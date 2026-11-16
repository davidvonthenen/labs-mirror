#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (NER + Rerank): chunk the curated recipe sample and index the chunks into TWO
indexes -- BM25 (`recipes-bm25`) and vector (`recipes-vector`) -- for an externally
orchestrated (not OpenSearch-native) two-phase retrieval pipeline.

Each recipe is split into chunks (see common/chunking.py). Every chunk goes into both indexes
with the same recipe-level fields (cautions, diet_labels, tier, ...), so a hard filter such as
"exclude sulfites" removes every chunk of a recipe that carries that caution.

Same "Recipe & Dietary Safety Assistant" theme as Baseline Vector RAG, Baseline BM25 RAG and Hybrid RAG (Native). Unlike
Hybrid RAG (Native)'s single native hybrid query, query.py runs BM25 first as a
*grounding* step (with hard structured filters -- see
tasks/hybrid-rag-ner-rerank.md) to build a provably-safe
candidate set, then re-ranks that set by vector similarity in
a second, separate query. Both indexes therefore need the same document
written into them, tagged with the same structured metadata (cautions,
diet_labels, etc.) so filters behave identically in either query.

`entities` (BM25-only) is populated the same way as Baseline BM25 RAG: NER-extracted
entities plus the recipe's own `source` and `recipe_name` folded in, since
spaCy's small model misses niche food-blog domains.
"""
from __future__ import annotations

import argparse
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.chunking import build_text, chunk_recipe, load_recipes
from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.ner_client import NERClient
from .common.opensearch_client import (
    create_client,
    drop_index,
    ensure_recipe_bm25_index,
    ensure_recipe_vector_index,
)

LOGGER = get_logger(__name__)

DEFAULT_TIER = "hot"


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the BM25 + vector indexes")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--bm25-index-name", type=str, default=None)
    parser.add_argument("--vector-index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32, help="Recipes per embedding batch")
    parser.add_argument(
        "--recreate", action="store_true",
        help="Delete both indexes first and rebuild them from empty (an index with the older one-document-per-recipe layout is replaced without this flag)",
    )
    return parser.parse_args(argv)


def _shared_fields(recipe: dict, chunk: dict) -> dict:
    """Fields common to both the BM25 and vector documents for one chunk.

    The recipe-level fields (cautions, labels, tier, ...) are copied onto every chunk of the recipe;
    the chunk fields (chunk_id, chunk_index, chunk_count, section, text) describe this one chunk.
    """
    return {
        "recipe_id": recipe["recipe_id"],
        "recipe_name": recipe["recipe_name"],
        "source": recipe.get("source"),
        "url": recipe.get("url"),
        "image_url": recipe.get("image_url"),
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
        "tier": DEFAULT_TIER,
    }


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/hybrid-rag-ner-rerank.md.")

    settings = load_settings()
    require_configured(settings)
    bm25_index = args.bm25_index_name or settings.opensearch_recipe_bm25_index
    vector_index = args.vector_index_name or settings.opensearch_recipe_vector_index

    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)
    if args.recreate:
        drop_index(client, bm25_index)
        drop_index(client, vector_index)
    ensure_recipe_bm25_index(client, bm25_index, replace_old_layout=True)
    ensure_recipe_vector_index(client, vector_index, embedder.dimension, replace_old_layout=True)

    recipes = load_recipes(data_file)

    total = 0
    chunk_total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            pairs = [(r, c) for r in batch for c in chunk_recipe(r)]
            embeddings = embedder.encode([c["text"] for _, c in pairs])
            # Entities come from the recipe's facts once, then go on every one of its chunks.
            entities_by_recipe = {
                r["recipe_id"]: normalize_values(
                    ner.extract_entities(build_text(r)) + [r["source"], r["recipe_name"]]
                )
                for r in batch
            }
            for (recipe, chunk), embedding in zip(pairs, embeddings):
                shared = _shared_fields(recipe, chunk)
                client.index(
                    index=bm25_index,
                    id=chunk["chunk_id"],
                    body={**shared, "entities": entities_by_recipe[recipe["recipe_id"]]},
                    refresh=False,
                )
                client.index(
                    index=vector_index,
                    id=chunk["chunk_id"],
                    body={**shared, "embedding": to_list(embedding)},
                    refresh=False,
                )
                chunk_total += 1
            progress.update(len(batch))
            total += len(batch)
    finally:
        progress.close()

    if total:
        client.indices.refresh(index=bm25_index)
        client.indices.refresh(index=vector_index)
    LOGGER.info("Ingested %d recipes as %d chunks into '%s' and '%s'", total, chunk_total, bm25_index, vector_index)


if __name__ == "__main__":
    main()
