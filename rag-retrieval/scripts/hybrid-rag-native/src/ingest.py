#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (Native): chunk, embed and index the curated recipe sample into a single native
OpenSearch hybrid index (text + embedding in one document, one index).

Each recipe is split into chunks (see common/chunking.py); every chunk is one document in this
index, with its text for BM25 and its embedding for kNN.

Same "Recipe & Dietary Safety Assistant" theme and dataset as Baseline Vector RAG and Baseline BM25 RAG,
but here there is no separate BM25 index, no separate vector index, and no
NER microservice to run alongside it. Every chunk's text starts with the recipe name and
source (see common/chunking.py), so plain BM25 keyword matching on
that field is enough to disambiguate by source -- the entity-tagging Baseline BM25 RAG
needed a whole Flask service for is unnecessary once BM25 and vector search
are fused into one native OpenSearch query. See
tasks/hybrid-rag-native.md for how the hybrid search pipeline
combines the two signals.
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
from .common.opensearch_client import create_client, drop_index, ensure_hybrid_index, ensure_hybrid_pipeline

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the hybrid index")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32, help="Recipes per embedding batch")
    parser.add_argument("--bm25-weight", type=float, default=0.5, help="Weight given to the BM25 sub-query")
    parser.add_argument("--vector-weight", type=float, default=0.5, help="Weight given to the kNN sub-query")
    parser.add_argument(
        "--recreate", action="store_true",
        help="Delete the index first and rebuild it from empty (an index with the older one-document-per-recipe layout is replaced without this flag)",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/hybrid-rag-native.md.")

    settings = load_settings()
    require_configured(settings)
    index_name = args.index_name or settings.opensearch_hybrid_index

    client = create_client(settings)
    embedder = EmbeddingModel(settings)
    if args.recreate:
        drop_index(client, index_name)
    ensure_hybrid_index(client, index_name, embedder.dimension, replace_old_layout=True)
    ensure_hybrid_pipeline(client, bm25_weight=args.bm25_weight, vector_weight=args.vector_weight)

    recipes = load_recipes(data_file)

    total = 0
    chunk_total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            pairs = [(r, c) for r in batch for c in chunk_recipe(r)]
            embeddings = embedder.encode([c["text"] for _, c in pairs])
            for (recipe, chunk), embedding in zip(pairs, embeddings):
                body = {
                    "recipe_id": recipe["recipe_id"],
                    "recipe_name": recipe["recipe_name"],
                    "source": recipe.get("source"),
                    "url": recipe.get("url"),
                    "chunk_id": chunk["chunk_id"],
                    "chunk_index": chunk["chunk_index"],
                    "chunk_count": chunk["chunk_count"],
                    "section": chunk["section"],
                    "text": chunk["text"],
                    "allergens": normalize_values(recipe.get("cautions")),
                    "diet_labels": normalize_values(recipe.get("diet_labels")),
                    "cuisine_type": normalize_values(recipe.get("cuisine_type")),
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
    LOGGER.info("Ingested %d recipes as %d chunks into hybrid index '%s'", total, chunk_total, index_name)


if __name__ == "__main__":
    main()
