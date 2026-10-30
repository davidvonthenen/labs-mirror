#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Hybrid RAG (Native). Run after ingest.py. No NER service is needed."""
from __future__ import annotations

from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def _top_sources(hits: list, n: int) -> list:
    return [(h.get("_source") or h).get("source") for h in hits[:n]]


def test_hybrid_rag_native_disambiguates_similar_questions() -> None:
    for source in (SOURCE_A, SOURCE_B):
        answer, hits = ask(f"Does the {source} Fish and Chips recipe contain sulfites?", top_k=5)
        assert hits, "Expected hybrid hits -- did ingest.py run?"
        assert answer, "Expected a non-empty LLM answer"
        top2 = _top_sources(hits, 2)
        assert source in top2, f"Expected {source}'s own recipe in the top 2 hits, got {top2}"
    print("[PASS] Hybrid RAG (Native) answered both disambiguation questions with hybrid-retrieved evidence")


if __name__ == "__main__":
    test_hybrid_rag_native_disambiguates_similar_questions()
