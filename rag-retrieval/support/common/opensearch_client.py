# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""OpenSearch client + index management shared by every pipeline.

All pipelines point at the same Instaclustr-hosted OpenSearch cluster (set once
via OPENSEARCH_HOST/PORT/USER/PASSWORD in .env). Each pipeline just uses a
different index name and mapping, defined below.
"""
from __future__ import annotations

import copy
from typing import Any, Dict, Iterable, List, Optional

from opensearchpy import OpenSearch
from opensearchpy.exceptions import NotFoundError, TransportError

from .config import Settings, load_settings
from .labels import normalize_values
from .logging import get_logger

LOGGER = get_logger(__name__)

VECTOR_FIELD = "embedding"


def create_client(settings: Optional[Settings] = None) -> OpenSearch:
    """Create the OpenSearch client used by every pipeline."""
    settings = settings or load_settings()
    http_auth = None
    if settings.opensearch_user and settings.opensearch_password:
        http_auth = (settings.opensearch_user, settings.opensearch_password)

    LOGGER.info(
        "Connecting to OpenSearch at %s:%s (ssl=%s)",
        settings.opensearch_host,
        settings.opensearch_port,
        settings.opensearch_ssl,
    )
    return OpenSearch(
        hosts=[{"host": settings.opensearch_host, "port": settings.opensearch_port}],
        http_auth=http_auth,
        use_ssl=settings.opensearch_ssl,
        verify_certs=settings.opensearch_ssl,
        ssl_show_warn=settings.opensearch_ssl,
        timeout=60,
        max_retries=3,
        retry_on_timeout=True,
    )


# ---------------------------------------------------------------------------
# Legacy helpers for an earlier news-article index schema (path/title/category/text).
# No task in this lab calls them: Baseline Vector RAG uses ensure_recipe_vector_index()
# and recipe_knn_search() further down. Kept only because they are exported below.
# ---------------------------------------------------------------------------

def ensure_vector_index(client: OpenSearch, index_name: str, dim: int) -> None:
    """Create a vector index for the legacy article schema (path/title/category/text + embedding). Unused by the tasks."""
    if client.indices.exists(index=index_name):
        return
    body = {
        "settings": {"index": {"knn": True}},
        "mappings": {
            "properties": {
                "path": {"type": "keyword"},
                "title": {"type": "keyword"},
                "category": {"type": "keyword"},
                "text": {"type": "text"},
                "embedding": {
                    "type": "knn_vector",
                    "dimension": dim,
                    "method": {"name": "hnsw", "space_type": "cosinesimil", "engine": "lucene"},
                },
            }
        },
    }
    LOGGER.info("Creating vector index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


def knn_search(client: OpenSearch, index: str, query_vec: List[float], k: int, num_candidates: Optional[int] = None):
    """Plain k-NN search over the legacy article schema (no metadata filters). Unused by the tasks."""
    body: Dict[str, Any] = {
        "size": k,
        "query": {"knn": {"embedding": {"vector": query_vec, "k": k}}},
        "_source": ["path", "title", "category", "text"],
    }
    if num_candidates and num_candidates > k:
        body["query"]["knn"]["embedding"]["rescore"] = {
            "oversample_factor": float(num_candidates) / float(max(k, 1))
        }
    return client.search(index=index, body=body)


# ---------------------------------------------------------------------------
# Legacy helpers for the same earlier article schema, BM25 side (content/category/explicit_terms).
# No task in this lab calls them: Baseline BM25 RAG uses ensure_recipe_bm25_index() and
# bm25_search() further down.
# ---------------------------------------------------------------------------

def ensure_bm25_index(client: OpenSearch, index_name: str, *, is_chunk_index: bool = False) -> None:
    """Create a BM25 full-document or chunk index for the legacy article schema. Unused by the tasks."""
    if client.indices.exists(index=index_name):
        return
    properties: Dict[str, Any] = {
        "content": {"type": "text"},
        "category": {"type": "keyword", "normalizer": "lowercase_normalizer"},
        "filepath": {"type": "keyword"},
        "explicit_terms": {"type": "keyword", "normalizer": "lowercase_normalizer"},
        "explicit_terms_text": {"type": "text"},
        "ingested_at_ms": {"type": "date", "format": "epoch_millis"},
    }
    if is_chunk_index:
        properties.update(
            {
                "chunk_index": {"type": "integer"},
                "chunk_count": {"type": "integer"},
                "parent_filepath": {"type": "keyword"},
            }
        )
    body = {
        "settings": {
            "analysis": {
                "normalizer": {
                    "lowercase_normalizer": {"type": "custom", "char_filter": [], "filter": ["lowercase"]}
                }
            },
            "number_of_replicas": 0,
        },
        "mappings": {"properties": properties},
    }
    LOGGER.info("Creating BM25 index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


def bm25_entity_query(question: str, entities: List[str]) -> Dict[str, Any]:
    """Build a dis_max query (strict entity match boosted over full-text) for the legacy article schema. Unused by the tasks."""
    if not entities:
        return {"dis_max": {"tie_breaker": 0.0, "queries": [{"match": {"content": {"query": question}}}]}}

    joined = " ".join(entities)
    strict_bool = {
        "bool": {
            "should": [
                {"terms_set": {"explicit_terms": {"terms": entities, "minimum_should_match_script": {"source": "params.num_terms"}}}},
                {"match": {"explicit_terms_text": {"query": joined, "operator": "and"}}},
                {"multi_match": {"query": joined, "fields": ["content^1.0", "category^0.5"], "operator": "and"}},
            ],
            "minimum_should_match": 1,
            "boost": 30.0,
        }
    }
    or_bool = {
        "bool": {
            "should": [
                {"terms": {"explicit_terms": entities}},
                {"match": {"explicit_terms_text": joined}},
                {"multi_match": {"query": joined, "fields": ["content^1.0", "category^0.5"]}},
            ],
            "minimum_should_match": 1,
            "boost": 10.0,
        }
    }
    return {"dis_max": {"tie_breaker": 0.0, "queries": [strict_bool, or_bool]}}


# ---------------------------------------------------------------------------
# Hybrid RAG (Native): single hybrid index (native OpenSearch hybrid pipeline, recipes)
# ---------------------------------------------------------------------------

def ensure_hybrid_index(client: OpenSearch, index_name: str, dim: int) -> None:
    """Create the Hybrid RAG (Native) single index with both a text field and a knn_vector field."""
    if client.indices.exists(index=index_name):
        return
    body = {
        "settings": {"index": {"knn": True}},
        "mappings": {
            "properties": {
                "recipe_id": {"type": "keyword"},
                "recipe_name": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
                "text": {"type": "text"},
                "allergens": {"type": "keyword"},
                "diet_labels": {"type": "keyword"},
                "cuisine_type": {"type": "keyword"},
                "embedding": {
                    "type": "knn_vector",
                    "dimension": dim,
                    "method": {"name": "hnsw", "space_type": "l2", "engine": "lucene"},
                },
            }
        },
    }
    LOGGER.info("Creating hybrid index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


HYBRID_PIPELINE_NAME = "hybrid-search-pipeline"


def ensure_hybrid_pipeline(client: OpenSearch, *, bm25_weight: float = 0.3, vector_weight: float = 0.7) -> None:
    """Create (or update) the native OpenSearch normalization/combination pipeline."""
    client.transport.perform_request(
        "PUT",
        f"/_search/pipeline/{HYBRID_PIPELINE_NAME}",
        body={
            "description": "Normalize and combine BM25 + kNN scores",
            "phase_results_processors": [
                {
                    "normalization-processor": {
                        "normalization": {"technique": "min_max"},
                        "combination": {
                            "technique": "arithmetic_mean",
                            "parameters": {"weights": [bm25_weight, vector_weight]},
                        },
                    }
                }
            ],
        },
    )
    LOGGER.info("Ensured hybrid search pipeline '%s'", HYBRID_PIPELINE_NAME)


def hybrid_search(client: OpenSearch, index: str, text_query: str, query_vector: List[float], k: int = 10):
    """Run one native hybrid (BM25 + kNN) query through the hybrid pipeline."""
    body = {
        "size": k,
        "query": {
            "hybrid": {
                "queries": [
                    {"match": {"text": text_query}},
                    {"knn": {VECTOR_FIELD: {"vector": query_vector, "k": k}}},
                ]
            }
        },
    }
    return client.search(index=index, body=body, params={"search_pipeline": HYBRID_PIPELINE_NAME})


# ---------------------------------------------------------------------------
# Hybrid RAG (NER + Rerank) and Governance Tiering: BM25-grounding +
# vector-refine recipe indexes with structured allergen/diet/cuisine filters
# ---------------------------------------------------------------------------

RECIPE_SOURCE_FIELDS = [
    "recipe_id",
    "recipe_name",
    "source",
    "url",
    "image_url",
    "servings",
    "calories",
    "chunk_id",
    "chunk_index",
    "chunk_count",
    "text",
    "entities",
    "cautions",
    "cautions_display",
    "health_labels",
    "health_labels_display",
    "diet_labels",
    "diet_labels_display",
    "cuisine_type",
    "meal_type",
    "dish_type",
    "tier",
]


def ensure_recipe_vector_index(client: OpenSearch, index_name: str, dim: int) -> None:
    if client.indices.exists(index=index_name):
        return
    body = {
        "settings": {"index": {"knn": True}},
        "mappings": {
            "properties": {
                "recipe_id": {"type": "keyword"},
                "recipe_name": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
                "source": {"type": "keyword"},
                "url": {"type": "keyword"},
                "image_url": {"type": "keyword"},
                "servings": {"type": "integer"},
                "calories": {"type": "float"},
                "chunk_id": {"type": "keyword"},
                "chunk_index": {"type": "integer"},
                "chunk_count": {"type": "integer"},
                "text": {"type": "text"},
                "cautions": {"type": "keyword"},
                "cautions_display": {"type": "keyword"},
                "diet_labels": {"type": "keyword"},
                "diet_labels_display": {"type": "keyword"},
                "health_labels": {"type": "keyword"},
                "health_labels_display": {"type": "keyword"},
                "cuisine_type": {"type": "keyword"},
                "meal_type": {"type": "keyword"},
                "dish_type": {"type": "keyword"},
                "tier": {"type": "keyword"},
                VECTOR_FIELD: {
                    "type": "knn_vector",
                    "dimension": dim,
                    "method": {"name": "hnsw", "space_type": "cosinesimil", "engine": "lucene"},
                },
            }
        },
    }
    LOGGER.info("Creating recipe vector index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


def ensure_recipe_bm25_index(client: OpenSearch, index_name: str) -> None:
    if client.indices.exists(index=index_name):
        return
    body = {
        "mappings": {
            "properties": {
                "recipe_id": {"type": "keyword"},
                "recipe_name": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
                "source": {"type": "keyword"},
                "url": {"type": "keyword"},
                "image_url": {"type": "keyword"},
                "servings": {"type": "integer"},
                "calories": {"type": "float"},
                "chunk_id": {"type": "keyword"},
                "chunk_index": {"type": "integer"},
                "chunk_count": {"type": "integer"},
                "text": {"type": "text"},
                "entities": {"type": "keyword"},
                "cautions": {"type": "keyword"},
                "cautions_display": {"type": "keyword"},
                "diet_labels": {"type": "keyword"},
                "diet_labels_display": {"type": "keyword"},
                "health_labels": {"type": "keyword"},
                "health_labels_display": {"type": "keyword"},
                "cuisine_type": {"type": "keyword"},
                "meal_type": {"type": "keyword"},
                "dish_type": {"type": "keyword"},
                "tier": {"type": "keyword"},
            }
        }
    }
    LOGGER.info("Creating recipe BM25 index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


# Field aliases exposed to the CLI's generalized --exclude/--require flags.
# Any of these fields can be excluded or required via
# `--exclude field=value` / `--require field=value`.
FILTERABLE_FIELDS = {
    "caution": "cautions",
    "allergen": "cautions",
    "diet": "diet_labels",
    "health": "health_labels",
    "cuisine": "cuisine_type",
    "meal": "meal_type",
    "dish": "dish_type",
}


def _resolve_field(alias: str) -> str:
    return FILTERABLE_FIELDS.get(alias.strip().lower(), alias.strip())


def parse_field_value_pairs(pairs: Iterable[str]) -> Dict[str, List[str]]:
    """Parse repeated ``field=value`` CLI args (e.g. ``allergen=Sulfites``)."""
    out: Dict[str, List[str]] = {}
    for pair in pairs or []:
        if "=" not in pair:
            raise ValueError(f"Expected field=value, got: {pair!r}")
        field, value = pair.split("=", 1)
        resolved = _resolve_field(field)
        out.setdefault(resolved, []).append(value.strip())
    return out


def build_recipe_bm25_query(
    query_text: str,
    *,
    k: int,
    entities: Optional[Iterable[str]] = None,
    include_recipe_ids: Optional[Iterable[str]] = None,
    exclude: Optional[Dict[str, List[str]]] = None,
    require: Optional[Dict[str, List[str]]] = None,
    tier: Optional[str] = None,
) -> Dict[str, Any]:
    """Build the BM25 grounding query with generalized structured filters."""
    text = str(query_text or "").strip()
    filters: List[Dict[str, Any]] = []
    must_not: List[Dict[str, Any]] = []
    should: List[Dict[str, Any]] = []

    recipe_ids = [str(x).strip() for x in (include_recipe_ids or []) if str(x).strip()]
    if recipe_ids:
        filters.append({"terms": {"recipe_id": recipe_ids}})

    if tier:
        filters.append({"term": {"tier": tier}})

    for field, values in (exclude or {}).items():
        normalized = normalize_values(values)
        if normalized:
            must_not.append({"terms": {field: normalized}})

    for field, values in (require or {}).items():
        normalized = normalize_values(values)
        for value in normalized:
            filters.append({"term": {field: value}})

    if entities:
        normalized_entities = normalize_values(entities)
        if normalized_entities:
            should.append(
                {
                    "constant_score": {
                        "filter": {"terms": {"entities": normalized_entities}},
                        "boost": 6.0,
                    }
                }
            )

    if text:
        must = [
            {
                "multi_match": {
                    "query": text,
                    "fields": ["recipe_name^4", "entities^3", "text^2", "source"],
                    "type": "best_fields",
                    "operator": "or",
                }
            }
        ]
    else:
        must = [{"match_all": {}}]

    bool_query: Dict[str, Any] = {"must": must, "filter": filters, "must_not": must_not}
    if should:
        bool_query["should"] = should

    return {"size": int(k), "_source": RECIPE_SOURCE_FIELDS, "query": {"bool": bool_query}}


def bm25_search(client: OpenSearch, index_name: str, query_text: str, **kwargs) -> Dict[str, Any]:
    body = build_recipe_bm25_query(query_text, **kwargs)
    try:
        return client.search(index=index_name, body=body, request_timeout=30)
    except TransportError as exc:
        LOGGER.warning("BM25 query failed on %s: %s", index_name, exc)
        return {"hits": {"total": {"value": 0, "relation": "eq"}, "hits": []}, "_error": str(exc)}


def build_recipe_vector_query(
    query_vector: List[float],
    *,
    k: int,
    candidate_k: Optional[int] = None,
    include_recipe_ids: Optional[Iterable[str]] = None,
    exclude: Optional[Dict[str, List[str]]] = None,
    require: Optional[Dict[str, List[str]]] = None,
    tier: Optional[str] = None,
) -> Dict[str, Any]:
    candidate_k = int(candidate_k or max(k, 25))
    filters: List[Dict[str, Any]] = []
    must_not: List[Dict[str, Any]] = []

    recipe_ids = [str(x) for x in (include_recipe_ids or []) if str(x).strip()]
    if recipe_ids:
        filters.append({"terms": {"recipe_id": recipe_ids}})

    if tier:
        filters.append({"term": {"tier": tier}})

    for field, values in (exclude or {}).items():
        normalized = normalize_values(values)
        if normalized:
            must_not.append({"terms": {field: normalized}})

    for field, values in (require or {}).items():
        normalized = normalize_values(values)
        if normalized:
            filters.append({"terms": {field: normalized}})

    return {
        "size": int(k),
        "_source": RECIPE_SOURCE_FIELDS,
        "query": {
            "bool": {
                "must": [{"knn": {VECTOR_FIELD: {"vector": query_vector, "k": candidate_k}}}],
                "filter": filters,
                "must_not": must_not,
            }
        },
    }


def recipe_knn_search(client: OpenSearch, index_name: str, query_vector: List[float], **kwargs) -> Dict[str, Any]:
    body = build_recipe_vector_query(query_vector, **kwargs)
    try:
        return client.search(index=index_name, body=body, request_timeout=30)
    except TransportError as exc:
        LOGGER.warning("Vector query failed on %s: %s", index_name, exc)
        return {"hits": {"total": {"value": 0, "relation": "eq"}, "hits": []}, "_error": str(exc)}


# ---------------------------------------------------------------------------
# Governance Tiering: governance audit trail for the hot/LT tiering policy
# ---------------------------------------------------------------------------


def ensure_audit_index(client: OpenSearch, index_name: str) -> None:
    """Create the Governance Tiering audit-log index: one document per query, recording
    the tiering decision and outcome for governance/compliance review."""
    if client.indices.exists(index=index_name):
        return
    body = {
        "mappings": {
            "properties": {
                "timestamp": {"type": "date"},
                "question": {"type": "text"},
                "entities": {"type": "keyword"},
                "exclude_filters": {"type": "keyword"},
                "require_filters": {"type": "keyword"},
                "tiers_searched": {"type": "keyword"},
                "escalated": {"type": "boolean"},
                "escalation_reason": {"type": "keyword"},
                "grounding_candidate_count": {"type": "integer"},
                "refined_hit_count": {"type": "integer"},
                "refined_recipe_ids": {"type": "keyword"},
                "refined_sources": {"type": "keyword"},
                "refined_cautions": {"type": "keyword"},
                "evidence_handles": {"type": "keyword"},
                "cited_handles": {"type": "keyword"},
                "hallucinated_citations": {"type": "keyword"},
                "citations_valid": {"type": "boolean"},
                "answer": {"type": "text"},
                "latency_ms": {"type": "float"},
            }
        }
    }
    LOGGER.info("Creating audit index '%s'", index_name)
    client.indices.create(index=index_name, body=body)


def log_audit_event(client: OpenSearch, index_name: str, event: Dict[str, Any]) -> None:
    """Write one governance audit record. Never raises -- a broken audit
    trail should be logged and investigated, but must not take down the
    user-facing query path."""
    try:
        client.index(index=index_name, body=event, refresh=True)
    except TransportError as exc:
        LOGGER.warning("Failed to write audit event to '%s': %s", index_name, exc)


# ---------------------------------------------------------------------------
# Operationalize Production: audit-log retention (ISM) + governance-signal monitoring queries
# ---------------------------------------------------------------------------

AUDIT_ISM_POLICY_ID = "recipes-audit-log-retention"


def ensure_audit_retention_policy(
    client: OpenSearch,
    index_name: str,
    *,
    retention_days: int = 180,
    policy_id: str = AUDIT_ISM_POLICY_ID,
) -> bool:
    """Attach an OpenSearch Index State Management (ISM) policy to the audit
    index so it can't grow unbounded -- an audit trail nobody has set a
    retention window on is a compliance liability, not an asset.

    Returns True if the policy is in place (created here, or already
    existed), False if the cluster doesn't support/expose ISM (some managed
    tiers restrict it). Callers should treat False as "flag for manual
    review", not as a fatal error -- see tasks/operationalize-production.md.
    """
    policy_body = {
        "policy": {
            "description": f"Delete '{index_name}' documents older than {retention_days} days",
            "default_state": "hot",
            "states": [
                {
                    "name": "hot",
                    "actions": [],
                    "transitions": [
                        {"state_name": "delete", "conditions": {"min_index_age": f"{int(retention_days)}d"}}
                    ],
                },
                {"name": "delete", "actions": [{"delete": {}}], "transitions": []},
            ],
            "ism_template": [{"index_patterns": [index_name], "priority": 100}],
        }
    }
    try:
        client.transport.perform_request("PUT", f"/_plugins/_ism/policies/{policy_id}", body=policy_body)
        LOGGER.info("Created ISM retention policy '%s' (%dd) for '%s'", policy_id, retention_days, index_name)
    except TransportError as exc:
        if getattr(exc, "status_code", None) == 409:
            LOGGER.info("ISM policy '%s' already exists", policy_id)
        else:
            LOGGER.warning(
                "Could not create ISM retention policy '%s' (ISM plugin may be unavailable on this "
                "cluster): %s",
                policy_id,
                exc,
            )
            return False

    try:
        client.transport.perform_request("POST", f"/_plugins/_ism/add/{index_name}", body={"policy_id": policy_id})
    except TransportError as exc:
        LOGGER.warning("Could not attach ISM policy '%s' to '%s': %s", policy_id, index_name, exc)
        return False

    LOGGER.info("Ensured ISM retention policy '%s' is attached to '%s'", policy_id, index_name)
    return True


def get_audit_retention_policy_status(client: OpenSearch, index_name: str) -> Optional[Dict[str, Any]]:
    """Return ISM's own explanation of which policy (if any) governs
    ``index_name``, or None if ISM isn't available/the call fails."""
    try:
        return client.transport.perform_request("GET", f"/_plugins/_ism/explain/{index_name}")
    except TransportError as exc:
        LOGGER.warning("Could not read ISM policy status for '%s': %s", index_name, exc)
        return None


def audit_log_governance_stats(client: OpenSearch, index_name: str, *, lookback_hours: int = 24) -> Dict[str, Any]:
    """Aggregate recipes-audit-log over the trailing window for governance
    monitoring: escalation rate (+ reasons breakdown) and the
    citation-hallucination rate. Returns zeroed-out stats (not an error) if
    the audit index doesn't exist yet or has no data in the window -- an
    empty audit trail is itself a signal worth surfacing, not a crash.
    """
    empty = {
        "total": 0,
        "escalated": 0,
        "escalation_rate": 0.0,
        "escalation_reasons": {},
        "citations_invalid": 0,
        "hallucination_rate": 0.0,
    }
    body = {
        "size": 0,
        "query": {"range": {"timestamp": {"gte": f"now-{int(lookback_hours)}h"}}},
        "aggs": {
            "escalated": {
                "filters": {
                    "filters": {
                        "true": {"term": {"escalated": True}},
                        "false": {"term": {"escalated": False}},
                    }
                }
            },
            "escalation_reasons": {"terms": {"field": "escalation_reason", "size": 20, "missing": "none"}},
            "citations_invalid": {"filter": {"term": {"citations_valid": False}}},
        },
    }
    try:
        response = client.search(index=index_name, body=body, request_timeout=30)
    except (TransportError, NotFoundError) as exc:
        LOGGER.warning("Could not query audit log '%s' for governance stats: %s", index_name, exc)
        return {**empty, "error": str(exc)}

    total = int(response.get("hits", {}).get("total", {}).get("value", 0))
    if not total:
        return empty

    aggs = response.get("aggregations", {})
    escalated = int(aggs.get("escalated", {}).get("buckets", {}).get("true", {}).get("doc_count", 0))
    reasons = {
        bucket["key"]: bucket["doc_count"]
        for bucket in aggs.get("escalation_reasons", {}).get("buckets", [])
        if bucket["key"] != "none"
    }
    citations_invalid = int(aggs.get("citations_invalid", {}).get("doc_count", 0))

    return {
        "total": total,
        "escalated": escalated,
        "escalation_rate": escalated / total,
        "escalation_reasons": reasons,
        "citations_invalid": citations_invalid,
        "hallucination_rate": citations_invalid / total,
    }


def fetch_hallucinated_audit_samples(
    client: OpenSearch, index_name: str, *, lookback_hours: int = 24, size: int = 5
) -> List[Dict[str, Any]]:
    """Fetch a few recent audit records with a hallucinated citation, for
    human review (surfaced by monitor.py alongside the aggregate count)."""
    body = {
        "size": size,
        "sort": [{"timestamp": {"order": "desc"}}],
        "query": {
            "bool": {
                "filter": [
                    {"range": {"timestamp": {"gte": f"now-{int(lookback_hours)}h"}}},
                    {"term": {"citations_valid": False}},
                ]
            }
        },
        "_source": ["timestamp", "question", "answer", "evidence_handles", "cited_handles", "hallucinated_citations"],
    }
    try:
        response = client.search(index=index_name, body=body, request_timeout=30)
    except (TransportError, NotFoundError) as exc:
        LOGGER.warning("Could not fetch hallucinated audit samples from '%s': %s", index_name, exc)
        return []
    return [hit.get("_source", {}) for hit in response.get("hits", {}).get("hits", [])]


def normalize_hits(response: Dict[str, Any]) -> List[Dict[str, Any]]:
    """Flatten OpenSearch hits into compact dicts for CLI/prompt use."""
    hits = response.get("hits", {}).get("hits", []) or []
    out = []
    for hit in hits:
        src = hit.get("_source", {})
        out.append({"score": float(hit.get("_score") or 0.0), "id": hit.get("_id") or "", **src})
    return out


__all__ = [
    "create_client",
    "ensure_vector_index",
    "knn_search",
    "ensure_bm25_index",
    "bm25_entity_query",
    "ensure_hybrid_index",
    "ensure_hybrid_pipeline",
    "hybrid_search",
    "HYBRID_PIPELINE_NAME",
    "ensure_recipe_vector_index",
    "ensure_recipe_bm25_index",
    "FILTERABLE_FIELDS",
    "parse_field_value_pairs",
    "build_recipe_bm25_query",
    "bm25_search",
    "build_recipe_vector_query",
    "recipe_knn_search",
    "normalize_hits",
    "RECIPE_SOURCE_FIELDS",
    "VECTOR_FIELD",
    "ensure_audit_index",
    "log_audit_event",
    "ensure_audit_retention_policy",
    "get_audit_retention_policy_status",
    "audit_log_governance_stats",
    "fetch_hallucinated_audit_samples",
    "AUDIT_ISM_POLICY_ID",
]
