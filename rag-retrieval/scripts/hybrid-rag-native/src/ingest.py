#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (Native): embed and index the curated recipe sample into a single native
OpenSearch hybrid index (text + embedding in one document, one index).

Same "Recipe & Dietary Safety Assistant" theme and dataset as Baseline Vector RAG and Baseline BM25 RAG,
but here there is no separate BM25 index, no separate vector index, and no
NER microservice to run alongside it. Every recipe's source name is already
baked into `text` (see build_text below), so plain BM25 keyword matching on
that field is enough to disambiguate by source -- the entity-tagging Baseline BM25 RAG
needed a whole Flask service for is unnecessary once BM25 and vector search
are fused into one native OpenSearch query. See
tasks/hybrid-rag-native.md for how the hybrid search pipeline
combines the two signals.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.opensearch_client import create_client, ensure_hybrid_index, ensure_hybrid_pipeline

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the hybrid index")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32)
    parser.add_argument("--bm25-weight", type=float, default=0.3, help="Weight given to the BM25 sub-query")
    parser.add_argument("--vector-weight", type=float, default=0.7, help="Weight given to the kNN sub-query")
    return parser.parse_args(argv)


def build_text(recipe: dict) -> str:
    """Render one recipe into the text that gets embedded AND BM25-matched.

    The source domain is folded directly into the text (not just stored as a
    separate keyword field) so that a question naming a specific source, e.g.
    "the thegratefulgirlcooks.com Fish and Chips recipe", has a literal term
    to match in the same `match` clause the hybrid query runs against.
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


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/hybrid-rag-native.md.")

    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    index_name = args.index_name or settings.opensearch_hybrid_index

    client = create_client(settings)
    embedder = EmbeddingModel(settings)
    ensure_hybrid_index(client, index_name, embedder.dimension)
    ensure_hybrid_pipeline(client, bm25_weight=args.bm25_weight, vector_weight=args.vector_weight)

    recipes = json.loads(data_file.read_text(encoding="utf-8"))

    total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            texts = [build_text(r) for r in batch]
            embeddings = embedder.encode(texts)
            for recipe, text, embedding in zip(batch, texts, embeddings):
                body = {
                    "recipe_id": recipe["recipe_id"],
                    "recipe_name": recipe["recipe_name"],
                    "source": recipe.get("source"),
                    "url": recipe.get("url"),
                    "text": text,
                    "allergens": normalize_values(recipe.get("cautions")),
                    "diet_labels": normalize_values(recipe.get("diet_labels")),
                    "cuisine_type": normalize_values(recipe.get("cuisine_type")),
                    "embedding": to_list(embedding),
                }
                client.index(index=index_name, id=recipe["recipe_id"], body=body, refresh=False)
                progress.update(1)
                total += 1
    finally:
        progress.close()

    if total:
        client.indices.refresh(index=index_name)
    LOGGER.info("Ingested %d recipes into hybrid index '%s'", total, index_name)


if __name__ == "__main__":
    main()
