#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Governance Tiering. Run after ingest.py (and with
ner_service.py running)."""
from __future__ import annotations

from .common.ner_client import NERClient
from .query import _extract_cited_handles, _extract_domain_tokens, ask


def _require_ner() -> None:
    """NERClient hides service failures, so without this the live tests could pass with NER down."""
    assert NERClient().is_available(), (
        "The NER service did not answer. Start it with `python -m src.ner_service` "
        "in scripts/baseline-bm25-rag, then rerun."
    )


def test_governance_tiering_citation_extraction_and_hallucination_detection() -> None:
    """Deterministic unit test for the citation-parsing mechanism itself, so
    this doesn't rely on a specific LLM happening to cite correctly."""
    answer = "The recipe uses sulfites [R1]. It also lists soba noodles [R2] [R1]."
    cited = _extract_cited_handles(answer)
    assert cited == ["R1", "R2"], cited

    evidence_handles = ["R1", "R2"]
    hallucinated = [h for h in cited if h not in evidence_handles]
    assert not hallucinated, hallucinated

    # A citation for a handle that was never retrieved must be caught.
    hallucinating_answer = "This recipe is safe [R1] and also gluten-free [R9]."
    cited2 = _extract_cited_handles(hallucinating_answer)
    hallucinated2 = [h for h in cited2 if h not in evidence_handles]
    assert hallucinated2 == ["R9"], hallucinated2
    print("[PASS] Governance Tiering citation extraction and hallucination detection work as expected")


def test_governance_tiering_source_detection_ignores_numbers_and_abbreviations() -> None:
    """Deterministic test (no live services): only real source domains count as a
    named source, so numbers and abbreviations do not force an escalation."""
    assert _extract_domain_tokens("Does the thegratefulgirlcooks.com recipe contain sulfites?") == [
        "thegratefulgirlcooks.com"
    ]
    assert _extract_domain_tokens("Is the womensweeklyfood.com.au recipe vegan?") == ["womensweeklyfood.com.au"]
    for question in ("A dish with 2.5 cups of flour.", "Suggest a dinner, e.g. pasta.", "Use version v1.2 of the plan."):
        assert _extract_domain_tokens(question) == [], question
    print("[PASS] Governance Tiering source detection ignores numbers and abbreviations")


def test_governance_tiering_hot_tier_recipe_resolves_without_escalation() -> None:
    """womensweeklyfood.com.au's Fish and Chips has a Sulfites caution, so
    ingest.py tiers it 'hot' -- it should be answerable without ever
    touching the LT tier."""
    _require_ner()
    answer, grounding_hits, refined_hits, meta = ask(
        "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?", top_k=5
    )
    assert refined_hits, "Expected hits for the hot-tier recipe"
    assert any(h.get("source") == "womensweeklyfood.com.au" for h in refined_hits), (
        f"Expected womensweeklyfood.com.au's recipe in refined hits: {refined_hits}"
    )
    assert answer, "Expected a non-empty Bedrock answer"
    assert not meta["escalated"], f"Did not expect escalation for a hot-tier recipe, got meta={meta}"
    assert meta["tiers_searched"] == ["hot"], meta
    assert meta["evidence_handles"], f"Expected citation handles for retrieved evidence, got meta={meta}"
    assert meta["cited_handles"], f"Expected the answer to cite at least one [R#] handle, got answer={answer!r}"
    assert meta["citations_valid"], f"Answer cited a handle that was never retrieved: meta={meta}"
    assert set(meta["cited_handles"]) <= set(meta["evidence_handles"]), meta
    print(f"[PASS] Governance Tiering answered the hot-tier recipe without escalating: meta={meta}")


def test_governance_tiering_lt_tier_recipe_triggers_escalation() -> None:
    """thegratefulgirlcooks.com's Fish and Chips has NO cautions, so
    ingest.py tiers it 'lt' -- a hot-only search can't find it by name, so
    the system must escalate to the full corpus to answer correctly."""
    _require_ner()
    answer, grounding_hits, refined_hits, meta = ask(
        "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?", top_k=5
    )
    assert refined_hits, "Expected hits after escalating to the LT tier"
    assert answer, "Expected a non-empty Bedrock answer"
    assert meta["escalated"], f"Expected escalation to reach the LT-tier recipe, got meta={meta}"
    assert "lt" in meta["tiers_searched"], meta
    found = any(h.get("source") == "thegratefulgirlcooks.com" for h in refined_hits)
    assert found, f"Expected thegratefulgirlcooks.com's recipe in refined hits: {refined_hits}"
    assert meta["evidence_handles"], f"Expected citation handles for retrieved evidence, got meta={meta}"
    assert meta["cited_handles"], f"Expected the answer to cite at least one [R#] handle, got answer={answer!r}"
    assert meta["citations_valid"], f"Answer cited a handle that was never retrieved: meta={meta}"
    assert set(meta["cited_handles"]) <= set(meta["evidence_handles"]), meta
    print(f"[PASS] Governance Tiering escalated to the LT tier and found the correct recipe: meta={meta}")


if __name__ == "__main__":
    test_governance_tiering_citation_extraction_and_hallucination_detection()
    test_governance_tiering_source_detection_ignores_numbers_and_abbreviations()
    test_governance_tiering_hot_tier_recipe_resolves_without_escalation()
    test_governance_tiering_lt_tier_recipe_triggers_escalation()
