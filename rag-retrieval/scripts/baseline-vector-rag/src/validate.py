#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Baseline Vector RAG. Run after ingest.py.

Besides checking that retrieval and generation work at all, this test asserts
the lesson of the task: for the thegratefulgirlcooks.com question, vector
search returns the *other* "Fish and Chips" recipe and drops the one that
answers the question. If the embedding model or the sample data change, that
assertion can stop holding; the failure message says so.
"""
from __future__ import annotations

from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def _sources(hits: list) -> list:
    return [h.get("source") for h in hits]


def test_baseline_vector_rag_returns_hits_and_answer() -> None:
    answer, hits = ask(
        f"Does the {SOURCE_A} Fish and Chips recipe contain sulfites?", top_k=5, num_candidates=25
    )
    assert answer, "Expected a non-empty answer from the LLM"
    assert len(hits) > 0, "Expected at least one retrieved recipe -- did ingest.py run?"
    print(f"[PASS] Baseline Vector RAG returned {len(hits)} hits and a {len(answer)}-char answer")


def test_baseline_vector_rag_drops_the_named_recipe() -> None:
    """The documented failure: the question about recipe A retrieves recipe B and not A."""
    _, hits_a = ask(f"Does the {SOURCE_A} Fish and Chips recipe contain sulfites?", top_k=5, num_candidates=25)
    assert SOURCE_B in _sources(hits_a), f"Expected {SOURCE_B}'s same-named recipe among the hits, got {_sources(hits_a)}"
    assert SOURCE_A not in _sources(hits_a), (
        f"{SOURCE_A}'s recipe was retrieved, so the documented vector-search gap did not reproduce. "
        f"Check the embedding model and the sample data. Sources: {_sources(hits_a)}"
    )
    _, hits_b = ask(f"Does the {SOURCE_B} Fish and Chips recipe contain sulfites?", top_k=5, num_candidates=25)
    assert SOURCE_B in _sources(hits_b), f"Expected {SOURCE_B}'s recipe among the hits, got {_sources(hits_b)}"
    print(
        f"[PASS] Baseline Vector RAG reproduced the retrieval gap: {SOURCE_A}'s recipe was not retrieved for its own "
        f"question, and {SOURCE_B}'s recipe was"
    )


if __name__ == "__main__":
    test_baseline_vector_rag_returns_hits_and_answer()
    test_baseline_vector_rag_drops_the_named_recipe()
