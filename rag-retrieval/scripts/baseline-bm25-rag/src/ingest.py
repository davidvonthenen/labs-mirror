#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline BM25 RAG: chunk and index the curated recipe sample for BM25 + NER-entity retrieval.

Each recipe is split into chunks (see common/chunking.py) and every chunk is indexed as its
own document, with the recipe's fields and entities copied onto it.

Every recipe is tagged with entities extracted by the local NER service,
plus its own source name folded in directly: spaCy's small model does not
reliably recognize niche food-blog/brand names (e.g. "Skinnytaste",
"sargento.com") as formal ORG entities, but the source is already known,
deterministic metadata -- so it's added to the same `entities` field NER
populates rather than left to chance.
"""
from __future__ import annotations

import argparse
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.chunking import build_text, chunk_recipe, load_recipes
from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.ner_client import NERClient
from .common.opensearch_client import create_client, drop_index, ensure_recipe_bm25_index

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample for BM25 retrieval")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    parser.add_argument(
        "--recreate", action="store_true",
        help="Delete the index first and rebuild it from empty (an index with the older one-document-per-recipe layout is replaced without this flag)",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/baseline-bm25-rag.md.")

    settings = load_settings()
    require_configured(settings)
    client = create_client(settings)
    ner = NERClient(settings)
    index_name = args.index_name or settings.opensearch_bm25_recipes_baseline_index
    if args.recreate:
        drop_index(client, index_name)
    ensure_recipe_bm25_index(client, index_name, replace_old_layout=True)

    recipes = load_recipes(data_file)

    count = 0
    chunk_count = 0
    for recipe in tqdm(recipes, desc="Indexing", unit="recipes"):
        # Entities come from the recipe's facts once, then go on every one of its chunks.
        ner_entities = ner.extract_entities(build_text(recipe))
        entities = normalize_values(ner_entities + [recipe["source"], recipe["recipe_name"]])
        for chunk in chunk_recipe(recipe):
            client.index(
                index=index_name,
                id=chunk["chunk_id"],
                body={
                    "recipe_id": recipe["recipe_id"],
                    "recipe_name": recipe["recipe_name"],
                    "source": recipe["source"],
                    "url": recipe.get("url"),
                    "servings": recipe.get("servings"),
                    "calories": recipe.get("calories"),
                    "chunk_id": chunk["chunk_id"],
                    "chunk_index": chunk["chunk_index"],
                    "chunk_count": chunk["chunk_count"],
                    "section": chunk["section"],
                    "text": chunk["text"],
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
            chunk_count += 1
        count += 1

    client.indices.refresh(index=index_name)
    LOGGER.info("Ingested %d recipes as %d chunks into '%s'", count, chunk_count, index_name)


if __name__ == "__main__":
    main()
