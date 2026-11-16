#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline Vector RAG: chunk, embed and index the curated recipe sample into Instaclustr OpenSearch.

Each recipe is split into chunks (ingredients, allergen cautions and labels, introduction,
method steps, serving notes -- see common/chunking.py) and every chunk is embedded and
indexed as its own document, with the recipe's own fields copied onto it.

Vector-only retrieval matches by *semantic similarity*, not by which
recipe is actually the correct one to answer about. See
tasks/baseline-vector-rag.md for the real, naturally-occurring
ambiguous pair this pipeline uses (two "Fish and
Chips" recipes from different sources, only one of which contains
sulfites).
"""
from __future__ import annotations

import argparse
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.chunking import chunk_recipe, load_recipes
from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.opensearch_client import create_client, drop_index, ensure_recipe_vector_index

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the vector index")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32, help="Recipes per embedding batch")
    parser.add_argument(
        "--recreate", action="store_true",
        help="Delete the index first and rebuild it from empty (an index with the older one-document-per-recipe layout is replaced without this flag)",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/baseline-vector-rag.md.")

    settings = load_settings()
    require_configured(settings)
    index_name = args.index_name or settings.opensearch_vector_recipes_baseline_index

    client = create_client(settings)
    embedder = EmbeddingModel(settings)
    if args.recreate:
        drop_index(client, index_name)
    ensure_recipe_vector_index(client, index_name, embedder.dimension, replace_old_layout=True)

    recipes = load_recipes(data_file)

    total = 0
    chunk_total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            # One (recipe, chunk) pair per document; every chunk is embedded on its own.
            pairs = [(r, c) for r in batch for c in chunk_recipe(r)]
            embeddings = embedder.encode([c["text"] for _, c in pairs])
            for (recipe, chunk), embedding in zip(pairs, embeddings):
                body = {
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
                    "cautions": normalize_values(recipe.get("cautions")),
                    "cautions_display": recipe.get("cautions") or [],
                    "diet_labels": normalize_values(recipe.get("diet_labels")),
                    "diet_labels_display": recipe.get("diet_labels") or [],
                    "health_labels": normalize_values(recipe.get("health_labels")),
                    "health_labels_display": recipe.get("health_labels") or [],
                    "cuisine_type": normalize_values(recipe.get("cuisine_type")),
                    "meal_type": normalize_values(recipe.get("meal_type")),
                    "dish_type": normalize_values(recipe.get("dish_type")),
                    "embedding": to_list(embedding),
                }
                client.index(index=index_name, id=chunk["chunk_id"], body=body, refresh=False)
                chunk_total += 1
            progress.update(len(batch))
            total += len(batch)
    finally:
        progress.close()

    if total:
        client.indices.refresh(index=index_name)
    LOGGER.info("Ingested %d recipes as %d chunks into index '%s'", total, chunk_total, index_name)


if __name__ == "__main__":
    main()
