#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Baseline Vector RAG. Run after ingest.py.

Besides checking that retrieval and generation work at all, this test asserts
the lesson of the task: for the thegratefulgirlcooks.com question, vector
search returns the *other* "Fish and Chips" recipe and drops the one that
answers the question. (The index holds chunks; hits are grouped back into recipes first.) If the embedding model or the sample data change, that
assertion can stop holding; the failure message says so.
"""
from __future__ import annotations

from .common.chunking import group_by_recipe, iter_chunks, load_recipes
from .common.config import RECIPES_SAMPLE_PATH, load_settings
from .common.embeddings import EmbeddingModel
from .query import ask

SOURCE_A = "thegratefulgirlcooks.com"  # Fish and Chips, no cautions
SOURCE_B = "womensweeklyfood.com.au"  # Fish and Chips, Sulfites


def _sources(hits: list) -> list:
    return [h.get("source") for h in hits]


def test_chunks_fit_the_embedding_model_and_name_their_recipe() -> None:
    """Deterministic (no cluster): every chunk must fit the embedding model's token limit, or the
    model silently cuts off the end of it, and every chunk must start with its recipe's name and
    source, or the two Fish and Chips recipes cannot be told apart from a chunk alone."""
    model = EmbeddingModel(load_settings()).model
    limit = model.max_seq_length
    recipes = load_recipes(RECIPES_SAMPLE_PATH)
    count = 0
    for recipe, chunk in iter_chunks(recipes):
        count += 1
        tokens = len(model.tokenizer(chunk["text"])["input_ids"])
        assert tokens <= limit, f"{chunk['chunk_id']} is {tokens} tokens, over the {limit}-token limit"
        assert chunk["text"].startswith(f"{recipe['recipe_name']} (source: {recipe['source']})"), chunk["chunk_id"]
    assert count > len(recipes), "Expected several chunks per recipe -- is recipes_synthetic.json present?"
    print(f"[PASS] {count} chunks from {len(recipes)} recipes all fit {limit} tokens and name their recipe")


def test_group_by_recipe_counts_recipes_not_chunks() -> None:
    """Deterministic (no cluster): five chunks of two recipes are two recipe hits, best score each."""
    hits = [
        {"recipe_id": "a", "score": 0.9, "chunk_index": 2, "section": "Introduction", "text": "A (source: s) - Introduction: hi"},
        {"recipe_id": "b", "score": 0.8, "chunk_index": 0, "section": "Ingredients", "text": "B (source: s) - Ingredients: x"},
        {"recipe_id": "a", "score": 0.7, "chunk_index": 0, "section": "Ingredients", "text": "A (source: s) - Ingredients: y"},
        {"recipe_id": "a", "score": 0.6, "chunk_index": 1, "section": "Allergen cautions and labels", "text": "A (source: s) - Allergen cautions and labels: z"},
        {"recipe_id": "a", "score": 0.5, "chunk_index": 3, "section": "Method steps 1-3", "text": "A (source: s) - Method steps 1-3: w"},
    ]
    grouped = group_by_recipe(hits, top_k=5, max_chunks=3)
    assert [g["recipe_id"] for g in grouped] == ["a", "b"], grouped
    assert grouped[0]["score"] == 0.9 and grouped[0]["matched_chunks"] == 4
    assert [c["chunk_index"] for c in grouped[0]["chunks"]] == [0, 1, 2], "evidence is the top 3 chunks in reading order"
    assert grouped[0]["text"].startswith("Ingredients: y"), grouped[0]["text"]
    print("[PASS] group_by_recipe folds chunk hits into recipes")


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
    test_chunks_fit_the_embedding_model_and_name_their_recipe()
    test_group_by_recipe_counts_recipes_not_chunks()
    test_baseline_vector_rag_returns_hits_and_answer()
    test_baseline_vector_rag_drops_the_named_recipe()
