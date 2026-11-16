#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Hybrid RAG (Native). Run after ingest.py. No NER service is needed."""
from __future__ import annotations

from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def _sources(hits: list) -> list:
    return [h.get("source") for h in hits]


def test_hybrid_rag_native_disambiguates_similar_questions() -> None:
    """Each question must put its named recipe in the top 2 hits, with no NER service.

    This holds at the default weights (BM25 0.5, vector 0.5). At the earlier 0.3 / 0.7 the vector side
    matched the "Allergen cautions" chunk of many unrelated recipes and pushed thegratefulgirlcooks.com's
    recipe to about 5th for its own question; see the weight sweep in tasks/hybrid-rag-native.md.
    """
    for source in (SOURCE_A, SOURCE_B):
        answer, hits = ask(f"Does the {source} Fish and Chips recipe contain sulfites?", top_k=5)
        assert hits, "Expected hybrid hits -- did ingest.py run?"
        assert answer, "Expected a non-empty LLM answer"
        top2 = _sources(hits)[:2]
        assert source in top2, f"Expected {source}'s own recipe in the top 2 hits, got {top2}"
    print("[PASS] Hybrid RAG (Native) answered both disambiguation questions with hybrid-retrieved evidence")


if __name__ == "__main__":
    test_hybrid_rag_native_disambiguates_similar_questions()
