#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Baseline BM25 RAG. Requires ner_service.py running and ingest.py already run."""
from __future__ import annotations

from .common.ner_client import NERClient
from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def test_baseline_bm25_rag_ner_service_is_running() -> None:
    """NERClient hides service failures, so without this check the test would pass with NER down."""
    assert NERClient().is_available(), (
        "The NER service did not answer. Start it with `python -m src.ner_service` "
        "in scripts/baseline-bm25-rag, then rerun."
    )
    print("[PASS] Baseline BM25 RAG NER service is running")


def test_baseline_bm25_rag_retrieves_both_same_named_recipes() -> None:
    a1, hits1 = ask(f"Does the {SOURCE_A} Fish and Chips recipe contain sulfites?", top_k=5)
    a2, hits2 = ask(f"Does the {SOURCE_B} Fish and Chips recipe contain sulfites?", top_k=5)
    assert a1 and a2, "Expected non-empty LLM answers for both questions"
    for name, hits in (("first", hits1), ("second", hits2)):
        sources = [h.get("source") for h in hits]
        assert SOURCE_A in sources and SOURCE_B in sources, (
            f"Expected both same-named Fish and Chips recipes among the {name} question's hits "
            f"-- did ingest.py run? Sources: {sources}"
        )
    print("[PASS] Baseline BM25 RAG retrieved both Fish and Chips recipes for both questions and answered each")


if __name__ == "__main__":
    test_baseline_bm25_rag_ner_service_is_running()
    test_baseline_bm25_rag_retrieves_both_same_named_recipes()
