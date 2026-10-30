#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Baseline BM25 RAG: BM25 + NER-entity retrieval over recipes, generation via Bedrock."""
from __future__ import annotations

import argparse
import time
from typing import List, Sequence

from .common import llm_client
from .common.config import load_settings, require_configured
from .common.ner_client import NERClient
from .common.opensearch_client import bm25_search, create_client, normalize_hits

SYSTEM_PROMPT = "Answer using ONLY the provided context below. If the context lacks the answer, say so plainly."


def _build_context(hits: List[dict], max_chars: int = 1200) -> str:
    parts = []
    for hit in hits:
        parts.append(
            f"---\nRecipe: {hit.get('recipe_name', '')} (source: {hit.get('source', '')})\n"
            f"{(hit.get('text') or '')[:max_chars]}\n"
        )
    return "\n".join(parts)


def ask(question: str, *, top_k: int) -> tuple[str, List[dict]]:
    settings = load_settings()
    require_configured(settings)
    client = create_client(settings)
    ner = NERClient(settings)
    index_name = settings.opensearch_bm25_recipes_baseline_index

    entities = ner.extract_entities(question)
    response = bm25_search(client, index_name, question, k=top_k, entities=entities)
    hits = normalize_hits(response)
    context_block = _build_context(hits)

    prompt = f"Context:\n{context_block}\n\nQuestion: {question}"
    answer = llm_client.generate(prompt, settings=settings, system=SYSTEM_PROMPT)
    return answer, hits


def main(argv: Sequence[str] | None = None) -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--question", "-q", type=str, help="Ask a single question and exit")
    parser.add_argument("--top-k", type=int, default=10)
    args = parser.parse_args(argv)

    questions = [args.question] if args.question else [
        "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?",
        "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?",
    ]

    for q in questions:
        t0 = time.time()
        answer, hits = ask(q, top_k=args.top_k)
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
