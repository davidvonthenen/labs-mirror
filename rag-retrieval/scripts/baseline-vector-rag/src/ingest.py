#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline Vector RAG: embed and index the curated recipe sample into Instaclustr OpenSearch.

Vector-only retrieval matches by *semantic similarity*, not by which
recipe is actually the correct one to answer about. See
tasks/baseline-vector-rag.md for the real, naturally-occurring
ambiguous pair this pipeline uses (two "Fish and
Chips" recipes from different sources, only one of which contains
sulfites).
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
from .common.opensearch_client import create_client, ensure_recipe_vector_index

LOGGER = get_logger(__name__)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ingest the curated recipe sample into the vector index")
    parser.add_argument(
        "--data-file", type=str, default=str(RECIPES_SAMPLE_PATH)
    )
    parser.add_argument("--index-name", type=str, default=None)
    parser.add_argument("--batch-size", type=int, default=32)
    return parser.parse_args(argv)


def build_text(recipe: dict) -> str:
    """Render one recipe into the text that gets embedded and shown to the LLM.

    Cautions/health labels are spelled out in plain English here (not just
    stored as structured fields) so that a question like "does it contain
    soy?" has the literal word "soy" to match against.
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
        raise FileNotFoundError(f"{data_file} not found. See tasks/baseline-vector-rag.md.")

    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    index_name = args.index_name or settings.opensearch_vector_recipes_baseline_index

    client = create_client(settings)
    embedder = EmbeddingModel(settings)
    ensure_recipe_vector_index(client, index_name, embedder.dimension)

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
                    "source": recipe["source"],
                    "url": recipe.get("url"),
                    "servings": recipe.get("servings"),
                    "calories": recipe.get("calories"),
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
                    "embedding": to_list(embedding),
                }
                client.index(index=index_name, id=recipe["recipe_id"], body=body, refresh=False)
                progress.update(1)
                total += 1
    finally:
        progress.close()

    if total:
        client.indices.refresh(index=index_name)
    LOGGER.info("Ingested %d recipes into index '%s'", total, index_name)


if __name__ == "__main__":
    main()
