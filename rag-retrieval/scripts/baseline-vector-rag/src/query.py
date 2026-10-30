#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline Vector RAG: ask questions against the recipe vector index using Bedrock for generation."""
from __future__ import annotations

import argparse
import time
from typing import List, Sequence

from .common import llm_client
from .common.config import load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.opensearch_client import create_client, ensure_recipe_vector_index, normalize_hits, recipe_knn_search

SYSTEM_PROMPT = (
    "You are a fact-focused recipe assistant. If the answer is not grounded in the "
    "snippets, respond with 'I don't know.' Be precise about which recipe and source "
    "you are answering about -- do not blend facts from different recipes together."
)


def _build_context(hits: List[dict]) -> str:
    parts = []
    for idx, hit in enumerate(hits, start=1):
        parts.append(
            f"[RECIPE {idx} | {hit.get('recipe_name', 'unknown')} | source: {hit.get('source', 'unknown')}]\n"
            f"{(hit.get('text') or '')[:900]}"
        )
    return "\n\n".join(parts)


def ask(question: str, *, top_k: int, num_candidates: int) -> tuple[str, List[dict]]:
    settings = load_settings()
    require_configured(settings)
    embedder = EmbeddingModel(settings)
    client = create_client(settings)
    index_name = settings.opensearch_vector_recipes_baseline_index
    ensure_recipe_vector_index(client, index_name, embedder.dimension)

    query_vec = to_list(embedder.encode([question])[0])
    response = recipe_knn_search(client, index_name, query_vec, k=top_k, candidate_k=num_candidates)
    hits = normalize_hits(response)
    context_block = _build_context(hits)

    prompt = f"Question:\n{question}\n\nContext:\n{context_block if context_block else 'No context available.'}"
    answer = llm_client.generate(prompt, settings=settings, system=SYSTEM_PROMPT)
    return answer, hits


def main(argv: Sequence[str] | None = None) -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--question", "-q", type=str, help="Ask a single question and exit")
    args = parser.parse_args(argv)

    settings = load_settings()
    questions = [args.question] if args.question else [
        "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?",
        "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?",
    ]

    for q in questions:
        t0 = time.time()
        answer, hits = ask(q, top_k=settings.rag_top_k, num_candidates=settings.rag_num_candidates)
        dt = time.time() - t0

        print("\n" + "=" * 88)
        print(f"QUESTION: {q}")
        print("=" * 88)
        print(f"ANSWER: {answer}")
        print("\nRAG Hits:")
        for hit in hits:
            print(
                f"  - {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| score={hit.get('score', 0.0):.4f}"
            )
        print("=" * 88)
        print(f"Query time: {dt:.2f}s | Docs provided to LLM: {len(hits)}\n")


if __name__ == "__main__":
    main()
