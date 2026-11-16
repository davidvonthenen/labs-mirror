#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (Native): ask questions against the native OpenSearch hybrid index.

One query, one index, one round trip: OpenSearch's `hybrid` query type runs
the BM25 `match` and the kNN sub-query in parallel, then the
`hybrid-search-pipeline` (set up in ingest.py) normalizes both score
distributions (min-max) and combines them (weighted arithmetic mean) before
results ever leave the cluster -- no client-side re-ranking, no second
service to run.

The index holds chunks, so the hybrid query returns chunks; `group_by_recipe` folds them back
into recipes (best chunk score per recipe) before they reach the LLM.
"""
from __future__ import annotations

import argparse
import time
from typing import List, Sequence

from .common import llm_client
from .common.chunking import chunk_fetch, group_by_recipe
from .common.config import load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.opensearch_client import (
    create_client,
    ensure_hybrid_index,
    ensure_hybrid_pipeline,
    hybrid_search,
    normalize_hits,
)

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
            f"{(hit.get('text') or '')[:1800]}"
        )
    return "\n\n".join(parts)


def ask(question: str, *, top_k: int) -> tuple[str, List[dict]]:
    settings = load_settings()
    require_configured(settings)
    embedder = EmbeddingModel(settings)
    client = create_client(settings)
    index_name = settings.opensearch_hybrid_index
    ensure_hybrid_index(client, index_name, embedder.dimension)
    ensure_hybrid_pipeline(client)

    query_vec = to_list(embedder.encode([question])[0])
    response = hybrid_search(client, index_name, question, query_vec, k=chunk_fetch(top_k))
    hits = group_by_recipe(normalize_hits(response), top_k=top_k)
    context_block = _build_context(hits)

    prompt = f"Question:\n{question}\n\nContext:\n{context_block if context_block else 'No context available.'}"
    answer = llm_client.generate(prompt, settings=settings, system=SYSTEM_PROMPT)
    return answer, hits


def main(argv: Sequence[str] | None = None) -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--question", "-q", type=str, help="Ask a single question and exit")
    parser.add_argument("--top-k", type=int, default=None)
    args = parser.parse_args(argv)

    settings = load_settings()
    top_k = args.top_k or settings.rag_top_k
    questions = [args.question] if args.question else [
        "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?",
        "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?",
    ]

    for q in questions:
        t0 = time.time()
        answer, hits = ask(q, top_k=top_k)
        dt = time.time() - t0

        print("\n" + "=" * 88)
        print(f"QUESTION: {q}")
        print("=" * 88)
        print(f"ANSWER: {answer}")
        print("\nRAG Hits (recipes, best chunk score; matching chunks in brackets):")
        for hit in hits:
            sections = ", ".join(c.get("section", "?") for c in hit.get("chunks", []))
            print(
                f"  - {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| score={hit.get('score', 0.0):.4f} | [{sections}]"
            )
        print("=" * 88)
        print(f"Query time: {dt:.2f}s | Recipes provided to LLM: {len(hits)}\n")


if __name__ == "__main__":
    main()
