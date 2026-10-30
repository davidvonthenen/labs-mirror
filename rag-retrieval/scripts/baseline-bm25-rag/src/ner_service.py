#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Local spaCy NER service used by the ingest/query paths of Baseline BM25 RAG
and Hybrid RAG (NER + Rerank).

Deliberately local-only: spaCy's small model is ~13MB and needs no GPU, so
there's no reason to move it to Instaclustr/Bedrock like the heavier pieces.

Endpoints
---------
POST /ner
    Request:  {"text": "...", "labels": ["ORG", "GPE"]}   # labels optional
    Response: {"entities": ["openai", "san francisco"], "entity_pairs": [...]}

Run
---
$ python -m src.ner_service
"""
import os
import uuid
from datetime import datetime, timezone
from functools import lru_cache
from typing import List, Tuple

import spacy
from flask import Flask, jsonify, request

SPACY_MODEL = os.getenv("SPACY_MODEL", "en_core_web_sm")

DEFAULT_INTERESTING_ENTITY_TYPES = {
    "PERSON",
    "ORG",
    "PRODUCT",
    "GPE",
    "EVENT",
    "WORK_OF_ART",
    "NORP",
    "LOC",
    "FAC",
}


@lru_cache(maxsize=1)
def load_spacy():
    return spacy.load(SPACY_MODEL)


nlp = load_spacy()

app = Flask(__name__)


def _extract_entities(nlp_obj, text: str, allowed_labels: set) -> List[Tuple[str, str]]:
    doc = nlp_obj(text)
    return [
        (ent.text.strip().lower(), ent.label_)
        for ent in doc.ents
        if ent.label_ in allowed_labels and len(ent.text.strip()) >= 3
    ]


@app.route("/ner", methods=["POST"])
def ner():
    data = request.get_json(silent=True)
    if not data or "text" not in data or not isinstance(data["text"], str):
        return jsonify({"error": "Invalid request", "detail": "Expected JSON with a 'text' string field."}), 400

    text = data["text"]
    labels_field = data.get("labels")
    allowed = {str(l).upper() for l in labels_field} if isinstance(labels_field, list) and labels_field else DEFAULT_INTERESTING_ENTITY_TYPES

    entity_pairs = _extract_entities(nlp, text, allowed)
    seen, normalized_entities = set(), []
    for name, _label in entity_pairs:
        if name not in seen:
            seen.add(name)
            normalized_entities.append(name)

    print(f"[{datetime.now(timezone.utc).isoformat()}] /ner: {len(text)} chars -> {len(normalized_entities)} entities")

    return jsonify(
        {
            "text": text,
            "model": SPACY_MODEL,
            "entities": normalized_entities,
            "entity_pairs": [{"name": n, "label": l} for n, l in entity_pairs],
            "request_id": str(uuid.uuid4()),
        }
    ), 200


if __name__ == "__main__":
    app.run(host="127.0.0.1", port=int(os.getenv("PORT", "8000")), debug=False)
