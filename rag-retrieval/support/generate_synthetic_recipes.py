# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Generate synthetic recipe-page text (intro, method, notes) for every recipe in recipes_sample.json.

Why: the recipe records hold an ingredient list and metadata but no page text, and many
of the original recipe URLs no longer work. The retrieval tasks chunk this text, so we
generate it once and commit the result (support/recipes/recipes_synthetic.json). Learners
do not need to run this script; it is only for regenerating the file.

The text is SYNTHETIC. It is written by an LLM from the ingredient list only, and it is
told not to mention allergens or add ingredients. Always run with --scan afterwards.

This script does not import the lab's `common` package, so it does not apply the macOS
certificate-bundle fix. Behind a TLS-inspecting proxy, set SSL_CERT_FILE / AWS_CA_BUNDLE
yourself (see troubleshooting/setup-environment.md) before running it.

Usage (from rag-retrieval/, with AWS credentials and BEDROCK_MODEL_ID in .env):
    python support/generate_synthetic_recipes.py            # generate missing recipes
    python support/generate_synthetic_recipes.py --scan     # check the saved file
    python support/generate_synthetic_recipes.py --force    # regenerate everything
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
RECIPES_PATH = HERE / "recipes" / "recipes_sample.json"
OUT_PATH = HERE / "recipes" / "recipes_synthetic.json"

META = {
    "synthetic": True,
    "note": (
        "Intro, method and notes are LLM-generated from each recipe's ingredient list. "
        "They are not copied from the original websites and are not tested recipes."
    ),
}

PROMPT = """Write the text of a recipe web page for the dish below.

Dish: {name}
Website: {source}
Ingredient list (use ONLY these ingredients; do not add any other ingredient):
{ingredients}

Rules:
- Return ONLY a JSON object with keys "intro" (string, 60-90 words), "method" (list of 6 to 9 strings, one step each, each 25-40 words), and "notes" (string, 60-90 words about serving and storage).
- Do not mention allergens, sulfites, or dietary suitability anywhere.
- Do not add any ingredient that is not in the list. Do not mention wine, vinegar, dried fruit or preservatives unless they are in the list.
- Do not mention the website name.
- Do not suggest serving the dish with, or pairing it with, any food or drink that is not in the ingredient list (no "serve with bread, rice, milk, yogurt, cheese" and so on). Describe serving only in terms of temperature, portion size, or the dish itself.
- Do not use the words vegan, vegetarian, gluten-free or dairy-free unless they are part of the dish name.
- Do not grease a pan with butter or oil, or add salt, water or any other item, unless it is in the ingredient list.
"""

# Words that would signal an invented allergen-relevant ingredient if they appear in the
# generated text but not in the recipe's own ingredient lines.
SCAN_TERMS = [
    "wine", "vinegar", "dried fruit", "raisin", "preservative", "sulfite", "sulphite",
    "milk", "cream", "butter", "cheese", "yogurt", "yoghurt", "egg",
    "peanut", "almond", "walnut", "pecan", "cashew", "hazelnut", "pistachio",
    "soy", "wheat", "flour", "breadcrumb", "bread",
    "shrimp", "prawn", "crab", "lobster", "clam", "mussel", "oyster", "fish", "cod", "salmon", "tuna",
    "sesame", "mustard", "celery", "gluten", "allergen", "allergy", "vegan", "vegetarian", "dairy",
]


def _load_env() -> None:
    try:
        from dotenv import load_dotenv
    except ImportError:  # python-dotenv is in requirements.txt; tolerate its absence
        return
    load_dotenv(HERE.parent / ".env")


def _client():
    import boto3

    return boto3.client("bedrock-runtime", region_name=os.environ["AWS_REGION"])


def _valid(sec: dict) -> bool:
    return (
        isinstance(sec.get("intro"), str)
        and isinstance(sec.get("notes"), str)
        and isinstance(sec.get("method"), list)
        and 4 <= len(sec["method"]) <= 12
        and all(isinstance(s, str) and s.strip() for s in sec["method"])
        and sec["intro"].strip()
        and sec["notes"].strip()
    )


def generate_one(client, recipe: dict, retries: int = 3) -> dict:
    prompt = PROMPT.format(
        name=recipe["recipe_name"],
        source=recipe["source"],
        ingredients="\n".join("- " + line for line in recipe["ingredient_lines"]),
    )
    body = {
        "anthropic_version": "bedrock-2023-05-31",
        "max_tokens": 1500,
        "temperature": 0.2,
        "messages": [{"role": "user", "content": prompt}],
    }
    last: Exception | None = None
    for attempt in range(retries):
        try:
            reply = json.loads(
                client.invoke_model(modelId=os.environ["BEDROCK_MODEL_ID"], body=json.dumps(body))["body"].read()
            )
            text = "".join(p.get("text", "") for p in reply["content"] if p.get("type") == "text")
            sec = json.loads(re.search(r"\{.*\}", text, re.S).group(0))
            if _valid(sec):
                return {"intro": sec["intro"].strip(), "method": [s.strip() for s in sec["method"]], "notes": sec["notes"].strip()}
            last = ValueError("response did not match the expected shape")
        except Exception as exc:  # noqa: BLE001 - retry on any transient or parse failure
            last = exc
        time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"{recipe['recipe_id']} ({recipe['recipe_name']}): {last}")


def _try(client, recipe: dict):
    try:
        return generate_one(client, recipe)
    except Exception as exc:  # noqa: BLE001
        print("  failed:", exc)
        return None


def word_count(sec: dict) -> int:
    return len(sec["intro"].split()) + sum(len(s.split()) for s in sec["method"]) + len(sec["notes"].split())


def flags_for(recipe: dict, sec: dict) -> list[str]:
    """Scan terms found as whole words in the text but absent from the recipe's ingredients and name."""
    ing = " ".join(recipe["ingredient_lines"]).lower() + " " + recipe["recipe_name"].lower()
    text = " ".join([sec["intro"], *sec["method"], sec["notes"]])
    text = re.sub(r"resembles (?:coarse |fine )?bread ?crumbs", "", text, flags=re.I)  # texture idiom, not an ingredient
    return [
        t for t in SCAN_TERMS
        if t not in ing and re.search(r"\b" + re.escape(t) + r"(?:s|es)?\b", text, re.I)
    ]


def scan(recipes: list[dict], saved: dict) -> int:
    """Flag synthetic text that mentions a scan term the recipe's own ingredients do not contain."""
    flagged = 0
    missing = [r["recipe_id"] for r in recipes if r["recipe_id"] not in saved]
    if missing:
        print(f"MISSING synthetic text for {len(missing)} recipes: {missing[:5]}...")
    for r in recipes:
        sec = saved.get(r["recipe_id"])
        if not sec:
            continue
        hits = flags_for(r, sec)
        if hits:
            flagged += 1
            print(f"  FLAG {r['recipe_id']} {r['recipe_name']!r} ({r['source']}): {hits}")
    counts = [word_count(s) for s in saved.values()]
    if counts:
        print(f"recipes with text: {len(saved)}/{len(recipes)}; words per recipe min/median/max: "
              f"{min(counts)}/{sorted(counts)[len(counts)//2]}/{max(counts)}")
    print(f"flagged for review: {flagged}")
    return flagged


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--scan", action="store_true", help="only scan the saved file")
    ap.add_argument("--force", action="store_true", help="regenerate every recipe")
    ap.add_argument("--fix", action="store_true", help="regenerate only the recipes the scan flags (up to 4 rounds)")
    ap.add_argument("--workers", type=int, default=6)
    ap.add_argument("--limit", type=int, default=0, help="only generate this many (for a trial)")
    args = ap.parse_args()

    recipes = json.loads(RECIPES_PATH.read_text())
    saved = json.loads(OUT_PATH.read_text())["recipes"] if OUT_PATH.exists() and not args.force else {}

    if args.scan:
        return 1 if scan(recipes, saved) else 0

    _load_env()
    if args.fix:
        client = _client()
        for round_no in range(1, 5):
            bad = [r for r in recipes if r["recipe_id"] in saved and flags_for(r, saved[r["recipe_id"]])]
            print(f"round {round_no}: {len(bad)} flagged")
            if not bad:
                break
            with ThreadPoolExecutor(max_workers=args.workers) as pool:
                for r, sec in zip(bad, pool.map(lambda r: _try(client, r), bad)):
                    if sec and len(flags_for(r, sec)) <= len(flags_for(r, saved[r["recipe_id"]])):
                        saved[r["recipe_id"]] = sec  # keep the new text only if it is no worse
            ordered = {r["recipe_id"]: saved[r["recipe_id"]] for r in recipes if r["recipe_id"] in saved}
            OUT_PATH.write_text(json.dumps({"_meta": META, "recipes": ordered}, indent=1))
        return 1 if scan(recipes, saved) else 0
    todo = [r for r in recipes if r["recipe_id"] not in saved]
    if args.limit:
        todo = todo[: args.limit]
    print(f"{len(saved)} already saved, generating {len(todo)}")
    client = _client()
    failures = []

    def work(r):
        try:
            return r["recipe_id"], generate_one(client, r), None
        except Exception as exc:  # noqa: BLE001
            return r["recipe_id"], None, str(exc)

    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        for i, (rid, sec, err) in enumerate(pool.map(work, todo), 1):
            if err:
                failures.append(err)
                print(f"  [{i}/{len(todo)}] FAILED {err}")
            else:
                saved[rid] = sec
            if i % 20 == 0:
                print(f"  [{i}/{len(todo)}] done")
                OUT_PATH.write_text(json.dumps({"_meta": META, "recipes": saved}, indent=1))

    # Keep the file in the same order as recipes_sample.json
    ordered = {r["recipe_id"]: saved[r["recipe_id"]] for r in recipes if r["recipe_id"] in saved}
    OUT_PATH.write_text(json.dumps({"_meta": META, "recipes": ordered}, indent=1))
    print(f"saved {len(ordered)} recipes to {OUT_PATH}; failures: {len(failures)}")
    scan(recipes, ordered)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
