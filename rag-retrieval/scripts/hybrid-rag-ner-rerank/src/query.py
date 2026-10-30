#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Hybrid RAG (NER + Rerank): BM25 grounding (hard filters + NER entities) -> vector-refine
rerank -> Bedrock generation.

This is deliberately NOT a single native OpenSearch query like Hybrid RAG (Native).
It's an *externally orchestrated* two-phase pipeline:

  1. GROUND (BM25, `recipes-bm25`): run a keyword/entity search that also
     applies hard `--exclude`/`--require` structured filters as real
     OpenSearch `must_not`/`filter` clauses. This produces a candidate set
     that is GUARANTEED to satisfy the constraints -- not just "usually
     ranks them lower", which is all pure similarity scoring (vector, or
     Hybrid RAG (Native)'s weighted hybrid) can offer for a negation like "no sulfites".
  2. REFINE (vector, `recipes-vector`): re-run the *same* constrained
     candidate set through kNN, restricted to just those recipe_ids, so the
     final ranking reflects semantic relevance to the actual question --
     without ever letting an excluded/non-conforming recipe back in.

See tasks/hybrid-rag-ner-rerank.md for why Hybrid RAG (Native)'s native
hybrid pipeline can't make the same guarantee.
"""
from __future__ import annotations

import argparse
import time
from typing import Dict, List, Optional, Sequence

from .common import llm_client
from .common.config import load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.ner_client import NERClient
from .common.opensearch_client import (
    bm25_search,
    create_client,
    normalize_hits,
    parse_field_value_pairs,
    recipe_knn_search,
)

SYSTEM_PROMPT = (
    "You are a fact-focused recipe assistant. If the answer is not grounded in the "
    "snippets, respond with 'I don't know.' Be precise about which recipe and source "
    "you are answering about -- do not blend facts from different recipes together. "
    "Never recommend a recipe that the context marks as containing an excluded allergen."
)


def _build_context(hits: List[dict]) -> str:
    parts = []
    for idx, hit in enumerate(hits, start=1):
        parts.append(
            f"[RECIPE {idx} | {hit.get('recipe_name', 'unknown')} | source: {hit.get('source', 'unknown')} | "
            f"cautions: {', '.join(hit.get('cautions_display') or []) or 'none listed'}]\n"
            f"{(hit.get('text') or '')[:900]}"
        )
    return "\n\n".join(parts)


def ask(
    question: str,
    *,
    top_k: int,
    candidate_k: int = 25,
    exclude: Optional[Dict[str, List[str]]] = None,
    require: Optional[Dict[str, List[str]]] = None,
) -> tuple[str, List[dict], List[dict]]:
    """Returns (answer, grounding_hits, refined_hits) -- both hit lists are
    exposed so callers/task-page examples can show the BM25->vector reordering.
    """
    settings = load_settings()
    require_configured(settings)
    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)

    bm25_index = settings.opensearch_recipe_bm25_index
    vector_index = settings.opensearch_recipe_vector_index

    entities = ner.extract_entities(question)
    grounding_response = bm25_search(
        client, bm25_index, question, k=candidate_k, entities=entities, exclude=exclude, require=require
    )
    grounding_hits = normalize_hits(grounding_response)
    candidate_ids = [h["recipe_id"] for h in grounding_hits if h.get("recipe_id")]

    if not candidate_ids:
        return (
            llm_client.generate(
                f"Question:\n{question}\n\nContext:\nNo recipes matched the requested constraints.",
                settings=settings,
                system=SYSTEM_PROMPT,
            ),
            grounding_hits,
            [],
        )

    query_vec = to_list(embedder.encode([question])[0])
    refine_response = recipe_knn_search(
        client,
        vector_index,
        query_vec,
        k=top_k,
        candidate_k=candidate_k,
        include_recipe_ids=candidate_ids,
        exclude=exclude,
        require=require,
    )
    refined_hits = normalize_hits(refine_response)
    context_block = _build_context(refined_hits)

    prompt = f"Question:\n{question}\n\nContext:\n{context_block if context_block else 'No context available.'}"
    answer = llm_client.generate(prompt, settings=settings, system=SYSTEM_PROMPT)
    return answer, grounding_hits, refined_hits


def main(argv: Sequence[str] | None = None) -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--question", "-q", type=str, help="Ask a single question and exit")
    parser.add_argument("--top-k", type=int, default=5)
    parser.add_argument("--candidate-k", type=int, default=25)
    parser.add_argument(
        "--exclude", action="append", default=[], metavar="field=value",
        help="Hard-exclude recipes matching field=value (repeatable), e.g. --exclude allergen=Sulfites",
    )
    parser.add_argument(
        "--require", action="append", default=[], metavar="field=value",
        help="Hard-require recipes matching field=value (repeatable), e.g. --require cuisine=british",
    )
    args = parser.parse_args(argv)

    exclude = parse_field_value_pairs(args.exclude)
    require = parse_field_value_pairs(args.require)

    questions = [args.question] if args.question else [
        "Suggest a British fish and chips recipe.",
    ]

    for q in questions:
        t0 = time.time()
        answer, grounding_hits, refined_hits = ask(
            q, top_k=args.top_k, candidate_k=args.candidate_k, exclude=exclude, require=require
        )
        dt = time.time() - t0

        print("\n" + "=" * 88)
        print(f"QUESTION: {q}")
        if exclude:
            print(f"EXCLUDE: {exclude}")
        if require:
            print(f"REQUIRE: {require}")
        print("=" * 88)
        print(f"ANSWER: {answer}")
        print(f"\nGROUNDING (BM25, {len(grounding_hits)} candidates, filters applied):")
        for hit in grounding_hits[:10]:
            print(
                f"  - {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| cautions={hit.get('cautions_display')} | score={hit.get('score', 0.0):.4f}"
            )
        print(f"\nREFINED (vector rerank of grounding set, top {len(refined_hits)}):")
        for hit in refined_hits:
            print(
                f"  - {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| cautions={hit.get('cautions_display')} | score={hit.get('score', 0.0):.4f}"
            )
        print("=" * 88)
        print(f"Query time: {dt:.2f}s | Docs provided to LLM: {len(refined_hits)}\n")


if __name__ == "__main__":
    main()
