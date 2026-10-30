#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline BM25 RAG: index the curated recipe sample for BM25 + NER-entity retrieval.

Every recipe is tagged with entities extracted by the local NER service,
plus its own source name folded in directly: spaCy's small model does not
reliably recognize niche food-blog/brand names (e.g. "Skinnytaste",
"sargento.com") as formal ORG entities, but the source is already known,
deterministic metadata -- so it's added to the same `entities` field NER
populates rather than left to chance.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.ner_client import NERClient
from .common.opensearch_client import create_client, ensure_recipe_bm25_index

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample for BM25 retrieval")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    return parser.parse_args(argv)


def build_text(recipe: dict) -> str:
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


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/baseline-bm25-rag.md.")

    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    client = create_client(settings)
    ner = NERClient(settings)
    index_name = args.index_name or settings.opensearch_bm25_recipes_baseline_index
    ensure_recipe_bm25_index(client, index_name)

    recipes = json.loads(data_file.read_text(encoding="utf-8"))

    count = 0
    for recipe in tqdm(recipes, desc="Indexing", unit="recipes"):
        text = build_text(recipe)
        ner_entities = ner.extract_entities(text)
        entities = normalize_values(ner_entities + [recipe["source"], recipe["recipe_name"]])
        client.index(
            index=index_name,
            id=recipe["recipe_id"],
            body={
                "recipe_id": recipe["recipe_id"],
                "recipe_name": recipe["recipe_name"],
                "source": recipe["source"],
                "url": recipe.get("url"),
                "servings": recipe.get("servings"),
                "calories": recipe.get("calories"),
                "text": text,
                "entities": entities,
                "cautions": normalize_values(recipe.get("cautions")),
                "cautions_display": recipe.get("cautions") or [],
                "diet_labels": normalize_values(recipe.get("diet_labels")),
                "diet_labels_display": recipe.get("diet_labels") or [],
                "health_labels": normalize_values(recipe.get("health_labels")),
                "health_labels_display": recipe.get("health_labels") or [],
                "cuisine_type": normalize_values(recipe.get("cuisine_type")),
                "meal_type": normalize_values(recipe.get("meal_type")),
                "dish_type": normalize_values(recipe.get("dish_type")),
            },
            refresh=False,
        )
        count += 1

    client.indices.refresh(index=index_name)
    LOGGER.info("Ingested %d recipes into '%s'", count, index_name)


if __name__ == "__main__":
    main()
