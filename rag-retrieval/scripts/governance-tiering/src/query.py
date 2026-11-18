#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Governance Tiering: hot-tier-first retrieval with governance-driven escalation to the
long-term (LT) tier, plus an audit trail of every tiering decision.

Builds directly on Hybrid RAG (NER + Rerank)'s ground (BM25) -> refine (vector) pipeline, over
recipe CHUNKS (see common/chunking.py). Searches return chunks and `group_by_recipe` folds them
back into recipes, so every count below -- hot hits, candidates, evidence handles -- counts
recipes, not chunks. One addition: the BM25 grounding step is first restricted to `tier=hot`
(fast, safety-critical data only). If that search isn't confident --  either
too few candidates, or the top score is weak -- the system ESCALATES to a
second grounding search across both tiers. Every query, regardless of
outcome, is written to the `recipes-audit-log` index: what was asked, which
tier(s) were searched, whether/why escalation happened, and what was
returned. That's the governance trail: you can always answer "did we search
the archive tier, and why (not)?" after the fact.

On top of that policy-level trail, this pipeline also adds output-level
citation-tagging: each recipe handed to the LLM is tagged with a stable
handle (`[R1]`, `[R2]`, ...), the system prompt requires every factual
sentence in the answer to cite the handle(s) that support it, and the
answer is parsed afterward to confirm it didn't cite a handle that was
never actually retrieved. That's a second, complementary governance
signal: not just "which tier did we search," but "which exact retrieved
chunk backs this specific claim" -- both are recorded in the same audit
record.
"""
from __future__ import annotations

import argparse
import re
import time
from datetime import datetime, timezone
from typing import Dict, List, Optional, Sequence, Tuple

from .common import llm_client
from .common.chunking import chunk_fetch, group_by_recipe
from .common.config import load_settings, require_configured
from .common.embeddings import EmbeddingModel, to_list
from .common.ner_client import NERClient
from .common.opensearch_client import (
    ann_pool_for_filtered_search,
    bm25_search,
    create_client,
    ensure_audit_index,
    log_audit_event,
    normalize_hits,
    parse_field_value_pairs,
    recipe_knn_search,
)

BASE_SYSTEM_PROMPT = (
    "You are a fact-focused recipe assistant. If the answer is not grounded in the "
    "snippets, respond with 'I don't know.' Be precise about which recipe and source "
    "you are answering about -- do not blend facts from different recipes together. "
    "Never recommend a recipe that the context marks as containing an excluded allergen."
)

_CITATION_RE = re.compile(r"\[(R\d+)\]")


# A dotted token counts as a source name only if it contains a letter and ends
# in an alphabetic label of 2+ letters (".com", ".com.au"). This skips numbers
# ("2.5"), version strings ("v1.2") and abbreviations ("e.g", "i.e"), which
# would otherwise be mistaken for a source and force an unnecessary escalation.
_DOMAIN_TOKEN_RE = re.compile(r"\b(?=[\w-]*[A-Za-z])[\w-]+(?:\.[\w-]+)*\.[A-Za-z]{2,}\b")


def _extract_domain_tokens(text: str) -> List[str]:
    """Pull out anything that looks like a source domain (e.g.
    "thegratefulgirlcooks.com") from a question. Numbers and abbreviations
    such as "2.5" or "e.g" are not domains and are ignored.

    A generic top BM25 score (boosted by `recipe_name`/`entities` matches on
    words like "fish and chips") can be high even when the ONE recipe the
    user actually named isn't in the hot-tier results at all -- so when the
    question names a specific source, we check for that directly rather
    than trusting the aggregate score alone.
    """
    return [m.group(0).lower() for m in _DOMAIN_TOKEN_RE.finditer(text)]


def _flatten_filters(filters: Optional[Dict[str, List[str]]]) -> List[str]:
    out = []
    for field, values in (filters or {}).items():
        for value in values:
            out.append(f"{field}={value}")
    return out


def _assign_handles(hits: List[dict]) -> List[dict]:
    """Tag each retrieved recipe with a stable citation handle (R1, R2, ...).

    The handle is what the LLM is required to cite in its answer, and what
    validate.py checks the answer against afterward -- it's the anchor that
    turns "which recipes did we retrieve" into "which recipe backs this
    specific sentence." Handle order matches `refined_hits` order, which is
    also the order audit_event's `refined_recipe_ids` is written in, so a
    handle can always be mapped back to a recipe ID from the audit log alone.
    """
    return [{**hit, "handle": f"R{idx}"} for idx, hit in enumerate(hits, start=1)]


def _build_context(hits: List[dict]) -> str:
    parts = []
    for hit in hits:
        handle = hit.get("handle") or "R?"
        parts.append(
            f"[{handle}]\n"
            f"recipe_name={hit.get('recipe_name', 'unknown')}, source={hit.get('source', 'unknown')}, "
            f"tier={hit.get('tier', 'unknown')}, "
            f"cautions={', '.join(hit.get('cautions_display') or []) or 'none listed'}\n"
            f"{(hit.get('text') or '')[:1800]}\n"
            f"[/{handle}]"
        )
    return "\n\n".join(parts)


def _build_system_prompt(allowed_handles: List[str]) -> str:
    """Extend the base system prompt with mandatory per-claim citations.

    Every snippet in the context is wrapped in a handle tag (see
    `_build_context`). Forcing the model to cite that handle after every
    factual sentence gives this pipeline's audit log a second, complementary
    governance signal on top of tiering: not just "which tier was searched,"
    but "which exact retrieved chunk backs this specific claim" -- and lets
    `validate.py` catch citations that don't correspond to real evidence.
    """
    if not allowed_handles:
        return (
            BASE_SYSTEM_PROMPT + " No recipe evidence was retrieved for this question, "
            "so there are no citation tags available -- say you don't know rather than "
            "guessing or inventing a citation."
        )
    allowed = " ".join(f"[{h}]" for h in allowed_handles)
    return (
        BASE_SYSTEM_PROMPT + " CITATION RULES (mandatory): each recipe snippet in the "
        f"context is wrapped in a handle tag like [R1]. Allowed citation tags for this "
        f"answer: {allowed}. After every sentence containing a factual recipe claim, "
        "append the [R#] tag(s) for the snippet(s) that support it. Use opening tags "
        "only (e.g. [R1]) -- never closing tags like [/R1]. Never invent a tag that is "
        "not in the allowed list above."
    )


def _extract_cited_handles(answer: str) -> List[str]:
    """Pull every [R#]-style citation tag out of the generated answer, in
    first-seen order, deduplicated."""
    seen: List[str] = []
    for match in _CITATION_RE.finditer(answer):
        tag = match.group(1)
        if tag not in seen:
            seen.append(tag)
    return seen


def ask(
    question: str,
    *,
    top_k: int,
    candidate_k: int = 25,
    exclude: Optional[Dict[str, List[str]]] = None,
    require: Optional[Dict[str, List[str]]] = None,
) -> Tuple[str, List[dict], List[dict], dict]:
    """Returns (answer, grounding_hits, refined_hits, governance_meta)."""
    settings = load_settings()
    require_configured(settings)
    client = create_client(settings)
    ner = NERClient(settings)
    embedder = EmbeddingModel(settings)

    bm25_index = settings.opensearch_recipe_bm25_index
    vector_index = settings.opensearch_recipe_vector_index
    audit_index = settings.opensearch_audit_index
    ensure_audit_index(client, audit_index)

    t0 = time.time()
    entities = ner.extract_entities(question)

    hot_response = bm25_search(
        client, bm25_index, question, k=chunk_fetch(candidate_k), entities=entities, exclude=exclude, require=require, tier="hot"
    )
    # Chunks in, recipes out: everything after this line counts recipes.
    hot_hits = group_by_recipe(normalize_hits(hot_response), top_k=candidate_k)
    top_score = hot_hits[0]["score"] if hot_hits else 0.0

    named_sources = _extract_domain_tokens(question)
    hot_sources = {(h.get("source") or "").lower() for h in hot_hits}
    named_source_missing = bool(named_sources) and not any(
        token in src for token in named_sources for src in hot_sources
    )

    escalate = (
        not hot_hits
        or len(hot_hits) < settings.tier_min_hot_candidates
        or top_score < settings.tier_min_hot_score
        or named_source_missing
    )
    escalation_reason = None
    if escalate:
        if not hot_hits:
            escalation_reason = "no_hot_candidates"
        elif named_source_missing:
            escalation_reason = "named_source_not_in_hot_tier"
        else:
            escalation_reason = "low_confidence_hot_score"
        full_response = bm25_search(
            client, bm25_index, question, k=chunk_fetch(candidate_k), entities=entities, exclude=exclude, require=require, tier=None
        )
        grounding_hits = group_by_recipe(normalize_hits(full_response), top_k=candidate_k)
        tiers_searched = ["hot", "lt"]
    else:
        grounding_hits = hot_hits
        tiers_searched = ["hot"]

    candidate_ids = [h["recipe_id"] for h in grounding_hits if h.get("recipe_id")]

    pinned_sources: List[str] = []
    if candidate_ids:
        query_vec = to_list(embedder.encode([question])[0])
        # The recipe_id filter runs after the kNN search, so the kNN step must consider every chunk.
        ann_pool = ann_pool_for_filtered_search(client, vector_index)
        refine_response = recipe_knn_search(
            client,
            vector_index,
            query_vec,
            k=chunk_fetch(top_k),
            candidate_k=ann_pool,
            include_recipe_ids=candidate_ids,
            exclude=exclude,
            require=require,
        )
        refined = group_by_recipe(normalize_hits(refine_response), top_k=top_k)

        # Named-source rule. A question like "does the X.com recipe contain sulfites?" matches the
        # allergen chunk of EVERY recipe, so the vector step can bury the recipe the user named even
        # though BM25 grounding ranked it first. If the question names a source and no refined recipe
        # comes from it, pin the best grounded recipe from that source into the evidence, using its
        # own best-matching chunks, and record the pin in the audit log.
        for token in named_sources:
            if any(token in (h.get("source") or "").lower() for h in refined):
                continue
            named = next((h for h in grounding_hits if token in (h.get("source") or "").lower()), None)
            if not named:
                continue
            pin_response = recipe_knn_search(
                client,
                vector_index,
                query_vec,
                k=chunk_fetch(1),
                candidate_k=ann_pool,
                include_recipe_ids=[named["recipe_id"]],
                exclude=exclude,
                require=require,
            )
            refined += group_by_recipe(normalize_hits(pin_response), top_k=1)
            pinned_sources.append(token)

        refined_hits = _assign_handles(refined)
        context_block = _build_context(refined_hits)
    else:
        refined_hits = []
        context_block = "No recipes matched the requested constraints."

    evidence_handles = [h["handle"] for h in refined_hits]
    # The exact chunks shown to the model, so the audit log can say which passage backed an answer.
    evidence_chunk_ids = [c.get("chunk_id") for h in refined_hits for c in h.get("chunks", []) if c.get("chunk_id")]
    system_prompt = _build_system_prompt(evidence_handles)
    prompt = f"Question:\n{question}\n\nContext:\n{context_block}"
    answer = llm_client.generate(prompt, settings=settings, system=system_prompt)
    latency_ms = (time.time() - t0) * 1000.0

    cited_handles = _extract_cited_handles(answer)
    hallucinated_citations = [h for h in cited_handles if h not in evidence_handles]
    citations_valid = not hallucinated_citations

    governance_meta = {
        "tiers_searched": tiers_searched,
        "escalated": escalate,
        "escalation_reason": escalation_reason,
        "hot_hit_count": len(hot_hits),
        "hot_top_score": top_score,
        "evidence_handles": evidence_handles,
        "evidence_chunk_ids": evidence_chunk_ids,
        "pinned_named_sources": pinned_sources,
        "cited_handles": cited_handles,
        "hallucinated_citations": hallucinated_citations,
        "citations_valid": citations_valid,
    }

    audit_event = {
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "question": question,
        "entities": entities,
        "exclude_filters": _flatten_filters(exclude),
        "require_filters": _flatten_filters(require),
        "tiers_searched": tiers_searched,
        "escalated": escalate,
        "escalation_reason": escalation_reason,
        "grounding_candidate_count": len(grounding_hits),
        "refined_hit_count": len(refined_hits),
        "refined_recipe_ids": [h.get("recipe_id") for h in refined_hits],
        "refined_sources": [h.get("source") for h in refined_hits if h.get("source")],
        "refined_cautions": sorted({c for h in refined_hits for c in (h.get("cautions_display") or [])}),
        "evidence_handles": evidence_handles,
        "evidence_chunk_ids": evidence_chunk_ids,
        "pinned_named_sources": pinned_sources,
        "cited_handles": cited_handles,
        "hallucinated_citations": hallucinated_citations,
        "citations_valid": citations_valid,
        "answer": answer,
        "latency_ms": latency_ms,
    }
    log_audit_event(client, audit_index, audit_event)

    return answer, grounding_hits, refined_hits, governance_meta


def main(argv: Sequence[str] | None = None) -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--question", "-q", type=str, help="Ask a single question and exit")
    parser.add_argument("--top-k", type=int, default=5)
    parser.add_argument("--candidate-k", type=int, default=25)
    parser.add_argument("--exclude", action="append", default=[], metavar="field=value")
    parser.add_argument("--require", action="append", default=[], metavar="field=value")
    args = parser.parse_args(argv)

    exclude = parse_field_value_pairs(args.exclude)
    require = parse_field_value_pairs(args.require)

    questions = [args.question] if args.question else [
        "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?",
        "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?",
    ]

    for q in questions:
        t0 = time.time()
        answer, grounding_hits, refined_hits, meta = ask(
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
        print(
            f"GOVERNANCE: tiers_searched={meta['tiers_searched']} escalated={meta['escalated']} "
            f"reason={meta['escalation_reason']} hot_hits={meta['hot_hit_count']} "
            f"hot_top_score={meta['hot_top_score']:.4f}"
        )
        if meta["pinned_named_sources"]:
            print(f"PINNED: named source(s) {meta['pinned_named_sources']} added to the evidence after the vector refine")
        print(
            f"CITATIONS: evidence={meta['evidence_handles']} cited={meta['cited_handles']} "
            f"hallucinated={meta['hallucinated_citations']} valid={meta['citations_valid']}"
        )
        print(f"ANSWER: {answer}")
        print(f"\nGROUNDING ({len(grounding_hits)} candidate recipes from tier(s) {meta['tiers_searched']}):")
        for hit in grounding_hits[:10]:
            print(
                f"  - {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| tier={hit.get('tier')} | cautions={hit.get('cautions_display')} | score={hit.get('score', 0.0):.4f}"
            )
        print(f"\nREFINED (vector rerank, top {len(refined_hits)} recipes; matching chunks in brackets):")
        for hit in refined_hits:
            sections = ", ".join(c.get("section", "?") for c in hit.get("chunks", []))
            print(
                f"  - [{hit.get('handle', '?')}] {hit.get('recipe_name', 'unknown')} | source={hit.get('source', 'unknown')} "
                f"| tier={hit.get('tier')} | cautions={hit.get('cautions_display')} | score={hit.get('score', 0.0):.4f} | [{sections}]"
            )
        print("=" * 88)
        print(f"Query time: {dt:.2f}s | Recipes provided to LLM: {len(refined_hits)}\n")


if __name__ == "__main__":
    main()
