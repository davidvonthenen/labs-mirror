# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Chunking for the Recipe & Dietary Safety Assistant (shared by every retrieval task).

Every retrieval task indexes and searches *chunks*, not whole recipes. A recipe is split
into its natural sections:

    Ingredients                    the ingredient list (split in parts if it is very long)
    Allergen cautions and labels   the safety facts, kept in a chunk of their own
    Introduction                   synthetic intro text
    Method steps 1-3, 4-6, ...     the synthetic method, three steps per chunk
    Serving and storage            synthetic serving / storage notes

Two rules make chunks work in this lab:

1. **Context-enriched chunks.** Every chunk's text starts with the recipe name and source,
   "<name> (source: <source>) - <section>: <body>". A chunk such as "Method steps 4-6" says
   nothing about which recipe it belongs to, so without the prefix the two "Fish and Chips"
   recipes become indistinguishable and a vector search cannot tell them apart.
2. **Recipe-level fields on every chunk.** `recipe_id`, `cautions`, `tier`, `source` and the
   other filter fields are copied onto every chunk, so hard filters such as
   "exclude sulfites" work on chunks exactly as they did on whole recipes.

Search returns chunks, so `group_by_recipe` folds the ranked chunks back into recipes (best
chunk score per recipe) before anything is shown to the LLM or counted.

The method, introduction and notes text is SYNTHETIC (see support/generate_synthetic_recipes.py);
the ingredients, cautions and labels come from the original recipe records.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

from .config import RECIPES_SYNTHETIC_PATH
from .logging import get_logger

LOGGER = get_logger(__name__)

# Method steps per chunk.
STEPS_PER_CHUNK = 3

# An ingredient list longer than this many characters is split into parts, so that no chunk
# is longer than the embedding model's 256-token limit (all-MiniLM-L6-v2 silently cuts off
# the rest). 700 characters is about 170 tokens, leaving room for the name/source prefix.
MAX_INGREDIENT_CHARS = 700

# A chunk search is asked for this many chunks per recipe you want back, because several
# chunks of one recipe often rank together (a dozen chunks can be only three recipes).
CHUNK_OVERFETCH = 6

# At most this many matching chunks per recipe are passed to the LLM as evidence.
MAX_EVIDENCE_CHUNKS = 3


def chunk_fetch(recipes_wanted: int) -> int:
    """How many chunks to ask the index for when you want `recipes_wanted` recipes back."""
    return int(recipes_wanted) * CHUNK_OVERFETCH


def build_text(recipe: dict) -> str:
    """The recipe's facts as one block of text: name, source, ingredients, cautions, labels.

    This is no longer what gets embedded or searched (chunks are). The BM25 tasks send it to
    the NER service once per recipe, so a recipe's `entities` come from its facts and are
    copied onto all of its chunks.
    """
    lines = recipe.get("ingredient_lines") or []
    cautions = recipe.get("cautions") or []
    health = recipe.get("health_labels") or []
    parts = [
        f"{recipe['recipe_name']} (source: {recipe['source']})",
        "Ingredients: " + "; ".join(lines) if lines else "",
        f"Allergen cautions: {', '.join(cautions)}." if cautions else "Allergen cautions: none listed.",
        f"Health labels: {', '.join(health)}." if health else "",
    ]
    return "\n".join(p for p in parts if p)


def chunk_prefix(recipe: dict, section: str) -> str:
    return f"{recipe['recipe_name']} (source: {recipe['source']}) - {section}: "


def _pack_lines(lines: List[str], max_chars: int) -> List[List[str]]:
    """Greedily pack ingredient lines into parts of at most `max_chars` characters."""
    parts: List[List[str]] = []
    current: List[str] = []
    size = 0
    for line in lines:
        if current and size + len(line) + 2 > max_chars:
            parts.append(current)
            current, size = [], 0
        current.append(line)
        size += len(line) + 2
    if current:
        parts.append(current)
    return parts


def _sections(recipe: dict) -> List[tuple[str, str]]:
    """(section name, body) pairs for one recipe, in reading order."""
    lines = [str(x).strip() for x in (recipe.get("ingredient_lines") or []) if str(x).strip()]
    cautions = recipe.get("cautions") or []
    health = recipe.get("health_labels") or []
    synthetic = recipe.get("synthetic") or {}

    out: List[tuple[str, str]] = []
    parts = _pack_lines(lines, MAX_INGREDIENT_CHARS)
    for i, part in enumerate(parts, start=1):
        out.append(("Ingredients" if len(parts) == 1 else f"Ingredients (part {i} of {len(parts)})", "; ".join(part)))

    safety = [f"Allergen cautions: {', '.join(cautions)}." if cautions else "Allergen cautions: none listed."]
    if health:
        safety.append(f"Health labels: {', '.join(health)}.")
    out.append(("Allergen cautions and labels", " ".join(safety)))

    if synthetic.get("intro"):
        out.append(("Introduction", synthetic["intro"].strip()))

    steps = [s.strip() for s in synthetic.get("method") or [] if s.strip()]
    for start in range(0, len(steps), STEPS_PER_CHUNK):
        group = steps[start : start + STEPS_PER_CHUNK]
        first, last = start + 1, start + len(group)
        label = f"Method step {first}" if first == last else f"Method steps {first}-{last}"
        out.append((label, " ".join(f"{start + i + 1}. {s}" for i, s in enumerate(group))))

    if synthetic.get("notes"):
        out.append(("Serving and storage", synthetic["notes"].strip()))
    return out


def chunk_recipe(recipe: dict) -> List[Dict[str, Any]]:
    """Split one recipe into chunks.

    Returns dicts with `chunk_id`, `chunk_index`, `chunk_count`, `section` and `text`
    (prefix included). Copy the recipe-level fields onto each chunk when indexing.
    """
    sections = _sections(recipe)
    total = len(sections)
    return [
        {
            "chunk_id": f"{recipe['recipe_id']}-{idx:02d}",
            "chunk_index": idx,
            "chunk_count": total,
            "section": section,
            "text": chunk_prefix(recipe, section) + body,
        }
        for idx, (section, body) in enumerate(sections)
    ]


def load_recipes(data_file: Path | str, synthetic_file: Path | str | None = None) -> List[dict]:
    """Load the recipe records and attach each recipe's synthetic text under `synthetic`.

    A recipe with no synthetic text still gets its Ingredients and Allergen chunks.
    """
    recipes = json.loads(Path(data_file).read_text(encoding="utf-8"))
    path = Path(synthetic_file) if synthetic_file else RECIPES_SYNTHETIC_PATH
    synthetic: Dict[str, dict] = {}
    if path.exists():
        synthetic = json.loads(path.read_text(encoding="utf-8")).get("recipes", {})
    else:
        LOGGER.warning("%s not found: recipes will only have Ingredients and Allergen chunks", path)
    missing = 0
    for recipe in recipes:
        sec = synthetic.get(recipe["recipe_id"])
        if sec:
            recipe["synthetic"] = sec
        else:
            missing += 1
    if missing and synthetic:
        LOGGER.warning("%d recipes have no synthetic text", missing)
    return recipes


def iter_chunks(recipes: Iterable[dict]) -> Iterable[tuple[dict, Dict[str, Any]]]:
    """Yield (recipe, chunk) for every chunk of every recipe."""
    for recipe in recipes:
        for chunk in chunk_recipe(recipe):
            yield recipe, chunk


def chunk_body(hit: dict) -> str:
    """A chunk hit's text without the name/source prefix (the prefix is for search, not for reading)."""
    text = hit.get("text") or ""
    section = hit.get("section")
    if section:
        marker = f" - {section}: "
        pos = text.find(marker)
        if pos != -1:
            return text[pos + len(marker) :]
    return text


def evidence_text(chunks: List[dict]) -> str:
    """The matching chunks of one recipe as readable evidence for the LLM."""
    return "\n".join(f"{c.get('section') or 'Text'}: {chunk_body(c)}" for c in chunks)


def group_by_recipe(
    hits: List[dict],
    *,
    top_k: Optional[int] = None,
    max_chunks: int = MAX_EVIDENCE_CHUNKS,
) -> List[dict]:
    """Fold ranked chunk hits into recipe hits.

    Each returned dict is the recipe's best-scoring chunk (so it carries the recipe-level
    fields) with:
      score           the best chunk score for that recipe
      chunks          up to `max_chunks` matching chunks, in reading order
      matched_chunks  how many chunks of this recipe were in the hits
      text            those chunks as readable evidence (see `evidence_text`)
    Recipes come back best first. Counting hits after this step counts recipes, not chunks.
    """
    by_recipe: Dict[str, List[dict]] = {}
    for hit in hits:
        rid = hit.get("recipe_id") or hit.get("id") or ""
        by_recipe.setdefault(rid, []).append(hit)

    grouped: List[dict] = []
    for chunks in by_recipe.values():
        ranked = sorted(chunks, key=lambda c: c.get("score", 0.0), reverse=True)
        best = ranked[0]
        shown = sorted(ranked[:max_chunks], key=lambda c: c.get("chunk_index", 0))
        grouped.append(
            {
                **best,
                "score": best.get("score", 0.0),
                "chunks": shown,
                "matched_chunks": len(chunks),
                "text": evidence_text(shown),
            }
        )
    grouped.sort(key=lambda g: g["score"], reverse=True)
    return grouped[:top_k] if top_k else grouped


__all__ = [
    "CHUNK_OVERFETCH",
    "MAX_EVIDENCE_CHUNKS",
    "MAX_INGREDIENT_CHARS",
    "STEPS_PER_CHUNK",
    "build_text",
    "chunk_body",
    "chunk_fetch",
    "chunk_prefix",
    "chunk_recipe",
    "evidence_text",
    "group_by_recipe",
    "iter_chunks",
    "load_recipes",
]
