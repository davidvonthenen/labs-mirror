#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Governance Tiering: (re)index the curated recipe sample into Hybrid RAG (NER + Rerank)'s SAME
`recipes-bm25` / `recipes-vector` indexes, this time assigning a real,
governance-driven `tier` value to every recipe instead of Hybrid RAG (NER + Rerank)'s
placeholder default of "hot" for everything.

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
import json
from collections import Counter
from pathlib import Path
from typing import Sequence

from tqdm import tqdm

from .common.config import RECIPES_SAMPLE_PATH, load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.labels import normalize_values
from .common.logging import get_logger
from .common.ner_client import NERClient
from .common.opensearch_client import (
    create_client,
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


def assign_tier(recipe: dict) -> str:
    """Governance policy: any allergen caution -> hot (always fast-reachable);
    no cautions listed -> lt (long-term/archive tier)."""
    return "hot" if recipe.get("cautions") else "lt"


def _shared_fields(recipe: dict, text: str) -> dict:
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
        "tier": assign_tier(recipe),
    }


def main(argv: Sequence[str] | None = None) -> None:
    args = parse_args(argv)
    data_file = Path(args.data_file)
    if not data_file.exists():
        raise FileNotFoundError(f"{data_file} not found. See tasks/governance-tiering.md.")

    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    bm25_index = args.bm25_index_name or settings.opensearch_recipe_bm25_index
    vector_index = args.vector_index_name or settings.opensearch_recipe_vector_index
    audit_index = args.audit_index_name or settings.opensearch_audit_index

    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)
    ensure_recipe_bm25_index(client, bm25_index)
    ensure_recipe_vector_index(client, vector_index, embedder.dimension)
    ensure_audit_index(client, audit_index)

    recipes = json.loads(data_file.read_text(encoding="utf-8"))

    tier_counts: Counter = Counter()
    total = 0
    progress = tqdm(total=len(recipes), desc="Indexing", unit="recipes")
    try:
        for batch_start in range(0, len(recipes), args.batch_size):
            batch = recipes[batch_start : batch_start + args.batch_size]
            texts = [build_text(r) for r in batch]
            embeddings = embedder.encode(texts)
            for recipe, text, embedding in zip(batch, texts, embeddings):
                shared = _shared_fields(recipe, text)
                tier_counts[shared["tier"]] += 1

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
    LOGGER.info(
        "Ingested %d recipes into '%s' and '%s' -- tier distribution: hot=%d, lt=%d",
        total,
        bm25_index,
        vector_index,
        tier_counts.get("hot", 0),
        tier_counts.get("lt", 0),
    )


if __name__ == "__main__":
    main()
