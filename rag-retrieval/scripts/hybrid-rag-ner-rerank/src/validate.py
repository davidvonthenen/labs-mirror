#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Hybrid RAG (NER + Rerank). Run after ingest.py (and with ner_service.py
from Baseline BM25 RAG running, since Hybrid RAG (NER + Rerank) reuses the same local NER service)."""
from __future__ import annotations

from .common.ner_client import NERClient
from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def _require_ner() -> None:
    """NERClient hides service failures, so without this the tests would pass with NER down."""
    assert NERClient().is_available(), (
        "The NER service did not answer. Start it with `python -m src.ner_service` "
        "in scripts/baseline-bm25-rag, then rerun."
    )


def test_hybrid_rag_ner_rerank_returns_hits_and_answer() -> None:
    _require_ner()
    answer, grounding_hits, refined_hits = ask("Suggest a British fish and chips recipe.", top_k=5)
    assert grounding_hits, "Expected BM25 grounding hits -- did ingest.py run?"
    assert refined_hits, "Expected vector-refined hits from the grounding candidate set"
    assert answer, "Expected a non-empty Bedrock answer"
    grounded = {h.get("source") for h in grounding_hits}
    assert {SOURCE_A, SOURCE_B} <= grounded, f"Expected both Fish and Chips recipes in the grounding set, got {grounded}"
    assert SOURCE_B in {h.get("source") for h in refined_hits}, "Expected the unfiltered query to surface the sulfite recipe"
    print(f"[PASS] Hybrid RAG (NER + Rerank) grounded {len(grounding_hits)} candidates and refined to {len(refined_hits)} hits")


def test_hybrid_rag_ner_rerank_hard_exclusion_filter_is_never_violated() -> None:
    """The teaching point of this pipeline: a hard --exclude filter must
    guarantee zero matching recipes in BOTH the grounding set and the
    final refined set -- not just down-rank them."""
    _require_ner()
    _, grounding_hits, refined_hits = ask(
        "Suggest a British fish and chips recipe.",
        top_k=5,
        exclude={"cautions": ["sulfites"]},
    )
    assert refined_hits, "Expected at least one recipe to satisfy the exclusion filter"
    assert SOURCE_A in {h.get("source") for h in refined_hits}, "Expected the sulfite-free Fish and Chips recipe to remain"
    assert SOURCE_B not in {h.get("source") for h in grounding_hits + refined_hits}, "The sulfite recipe leaked through"
    for hit in grounding_hits:
        assert "sulfites" not in (hit.get("cautions") or []), f"Excluded allergen leaked into grounding: {hit}"
    for hit in refined_hits:
        assert "sulfites" not in (hit.get("cautions") or []), f"Excluded allergen leaked into refined results: {hit}"
    print(
        f"[PASS] Hybrid RAG (NER + Rerank) enforced the sulfite exclusion filter across "
        f"{len(grounding_hits)} grounding hits and {len(refined_hits)} refined hits"
    )


if __name__ == "__main__":
    test_hybrid_rag_ner_rerank_returns_hits_and_answer()
    test_hybrid_rag_ner_rerank_hard_exclusion_filter_is_never_violated()
