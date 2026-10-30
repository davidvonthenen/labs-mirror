#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (NER + Rerank): index the curated recipe sample into TWO indexes -- BM25
(`recipes-bm25`) and vector (`recipes-vector`) -- for an externally
orchestrated (not OpenSearch-native) two-phase retrieval pipeline.

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
import json
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.ner_client import NERClient
from .common.opensearch_client import create_client, ensure_recipe_bm25_index, ensure_recipe_vector_index

LOGGER = get_logger(__name__)

DEFAULT_TIER = "hot"


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the BM25 + vector indexes")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--bm25-index-name", type=str, default=None)
    parser.add_argument("--vector-index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32)
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


def _shared_fields(recipe: dict, text: str) -> dict:
    """Fields common to both the BM25 and vector documents for one recipe.

    chunk_id/chunk_index/chunk_count are always a single chunk here (one
    document per recipe, like Baseline Vector RAG and Baseline BM25 RAG) -- they exist in the schema so a
    future version could split a recipe's instructions into multiple chunks
    without changing the index mapping or query helpers.
    """
    return {
        "recipe_id": recipe["recipe_id"],
        "recipe_name": recipe["recipe_name"],
        "source": recipe.get("source"),
        "url": recipe.get("url"),
        "image_url": recipe.get("image_url"),
        "servings": recipe.get("servings"),
        "calories": recipe.get("calories"),
        "chunk_id": recipe["recipe_id"],
        "chunk_index": 0,
        "chunk_count": 1,
        "text": text,
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
    require_configured(settings, need_bedrock=False)
    bm25_index = args.bm25_index_name or settings.opensearch_recipe_bm25_index
    vector_index = args.vector_index_name or settings.opensearch_recipe_vector_index

    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)
    ensure_recipe_bm25_index(client, bm25_index)
    ensure_recipe_vector_index(client, vector_index, embedder.dimension)

    recipes = json.loads(data_file.read_text(encoding="utf-8"))

    total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            texts = [build_text(r) for r in batch]
            embeddings = embedder.encode(texts)
            for recipe, text, embedding in zip(batch, texts, embeddings):
                shared = _shared_fields(recipe, text)

                ner_entities = ner.extract_entities(text)
                entities = normalize_values(ner_entities + [recipe["source"], recipe["recipe_name"]])
                client.index(
                    index=bm25_index,
                    id=recipe["recipe_id"],
                    body={**shared, "entities": entities},
                    refresh=False,
                )

                client.index(
                    index=vector_index,
                    id=recipe["recipe_id"],
                    body={**shared, "embedding": to_list(embedding)},
                    refresh=False,
                )
                progress.update(1)
                total += 1
    finally:
        progress.close()

    if total:
        client.indices.refresh(index=bm25_index)
        client.indices.refresh(index=vector_index)
    LOGGER.info("Ingested %d recipes into '%s' and '%s'", total, bm25_index, vector_index)


if __name__ == "__main__":
    main()
