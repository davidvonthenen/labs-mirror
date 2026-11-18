#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Governance Tiering: (re)index the curated recipe sample, as chunks, into Hybrid RAG (NER + Rerank)'s
SAME `recipes-bm25` / `recipes-vector` indexes, this time assigning a real,
governance-driven `tier` value to every recipe instead of Hybrid RAG (NER + Rerank)'s
placeholder default of "hot" for everything.

The tier belongs to the RECIPE, so it is copied onto every chunk of that recipe, and the tier
counts this script logs are counts of recipes, not of chunks.

Tiering policy (the "governance" half of this pipeline's name):
    tier = "hot" if the recipe carries ANY allergen caution, else "lt".

Safety-relevant data is never allowed to be "just" in the long-term/archive
tier -- it must always be reachable through the fast, default search path.
Recipes with no caution flags are lower-priority and are relegated to "lt",
which query.py only searches when the hot tier isn't confident it has the
answer (see query.py / tasks/governance-tiering.md for the
escalation logic).

Also creates the Governance Tiering audit-log index (`recipes-audit-log`) that
query.py writes one governance record to per question.
"""
from __future__ import annotations

import argparse
from collections import Counter
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
    ensure_audit_index,
    ensure_recipe_bm25_index,
    ensure_recipe_vector_index,
)

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample with hot/LT tier assignment")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--bm25-index-name", type=str, default=None)
    parser.add_argument("--vector-index-name", type=str, default=None)
    parser.add_argument("--audit-index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32, help="Recipes per embedding batch")
    parser.add_argument(
        "--recreate", action="store_true",
        help="Delete both recipe indexes first and rebuild them from empty (an index with the older one-document-per-recipe layout is replaced without this flag)",
    )
    return parser.parse_args(argv)


def assign_tier(recipe: dict) -> str:
    """Governance policy: any allergen caution -> hot (always fast-reachable);
    no cautions listed -> lt (long-term/archive tier)."""
    return "hot" if recipe.get("cautions") else "lt"


def _shared_fields(recipe: dict, chunk: dict) -> dict:
    """Fields common to both documents of one chunk: the recipe's fields (including its tier) plus the chunk's own."""
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
        "tier": assign_tier(recipe),
    }


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/governance-tiering.md.")

    settings = load_settings()
    require_configured(settings)
    bm25_index = args.bm25_index_name or settings.opensearch_recipe_bm25_index
    vector_index = args.vector_index_name or settings.opensearch_recipe_vector_index
    audit_index = args.audit_index_name or settings.opensearch_audit_index

    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)
    if args.recreate:
        drop_index(client, bm25_index)
        drop_index(client, vector_index)
    ensure_recipe_bm25_index(client, bm25_index, replace_old_layout=True)
    ensure_recipe_vector_index(client, vector_index, embedder.dimension, replace_old_layout=True)
    ensure_audit_index(client, audit_index)

    recipes = load_recipes(data_file)

    tier_counts: Counter = Counter()  # counted per recipe, not per chunk
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
            for recipe in batch:
                tier_counts[assign_tier(recipe)] += 1
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
    LOGGER.info(
        "Ingested %d recipes as %d chunks into '%s' and '%s' -- tier distribution (recipes): hot=%d, lt=%d",
        total,
        chunk_total,
        bm25_index,
        vector_index,
        tier_counts.get("hot", 0),
        tier_counts.get("lt", 0),
    )


if __name__ == "__main__":
    main()
