# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Central runtime configuration for every pipeline of the RAG journey.

Every pipeline accepts the inherited environment and an optional .env file, so
the only thing that changes between pipelines is which index/model fields they
read -- not how they connect to Instaclustr OpenSearch or the selected LLM.
"""
from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from dotenv import load_dotenv

from . import LAB_ROOT

# Curated 260-recipe sample shared by every ingest script (support/recipes/).
RECIPES_SAMPLE_PATH = LAB_ROOT / "support/recipes/recipes_sample.json"
# Synthetic intro / method / notes text for the same 260 recipes, keyed by recipe_id.
# Chunking (common/chunking.py) splits this text and the recipe facts into the chunks
# that every pipeline indexes and searches.
RECIPES_SYNTHETIC_PATH = LAB_ROOT / "support/recipes/recipes_synthetic.json"

_ENV_LOADED = False


def _load_env_once() -> None:
    global _ENV_LOADED
    if _ENV_LOADED:
        return
    for candidate in (Path(".env"), LAB_ROOT / ".env"):
        if candidate.exists():
            load_dotenv(str(candidate))
            break
    _ENV_LOADED = True


def _get_bool(name: str, default: bool) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.strip().lower() in ("1", "true", "yes", "on")


def _get_int(name: str, default: int) -> int:
    value = os.getenv(name)
    if value is None or value == "":
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _get_float(name: str, default: float) -> float:
    value = os.getenv(name)
    if value is None or value == "":
        return default
    try:
        return float(value)
    except ValueError:
        return default


def _get_optional_float(name: str) -> Optional[float]:
    value = os.getenv(name)
    if value is None or not value.strip():
        return None
    try:
        return float(value)
    except ValueError as exc:
        raise RuntimeError(f"{name} must be a number or left unset.") from exc


def _get_str(name: str, default: str) -> str:
    return os.getenv(name, default)


def _clear_blank_aws_profiles() -> None:
    # Compose and coding sessions can export empty optional values. Boto3 treats
    # an empty profile as a named profile and rejects otherwise valid credentials.
    for name in ("AWS_PROFILE", "AWS_DEFAULT_PROFILE"):
        value = os.environ.get(name)
        if value is not None and not value.strip():
            os.environ.pop(name, None)


@dataclass
class Settings:
    """Runtime configuration shared by every pipeline."""

    # --- Instaclustr OpenSearch ---
    opensearch_host: str = "127.0.0.1"
    opensearch_port: int = 9200
    opensearch_user: str = ""
    opensearch_password: str = ""
    opensearch_ssl: bool = True

    # Per-pipeline default index names (each pipeline can still override via CLI/env)
    opensearch_hybrid_index: str = "recipes-hybrid"
    opensearch_recipe_bm25_index: str = "recipes-bm25"
    opensearch_recipe_vector_index: str = "recipes-vector"
    # Baseline Vector RAG and Baseline BM25 RAG: baseline vector/BM25 indexes
    # over the recipe dataset. Kept as separate indexes so they never collide
    # with the richer recipe indexes used by Hybrid RAG (NER + Rerank) and
    # Governance Tiering above (opensearch_recipe_bm25_index /
    # opensearch_recipe_vector_index).
    opensearch_vector_recipes_baseline_index: str = "recipes-vector-baseline"
    opensearch_bm25_recipes_baseline_index: str = "recipes-bm25-baseline"
    # Governance Tiering: governance audit trail (every query's tiering decision + result)
    opensearch_audit_index: str = "recipes-audit-log"
    search_preference: str = "rag-retrieval"

    # --- Governance Tiering: hot/long-term (LT) tiering governance policy ---
    # A recipe is "hot" (always searched first) if it carries any allergen
    # caution -- safety-critical data must never be relegated to a
    # slower/optional fallback tier. Recipes with no cautions are "lt"
    # (archival) and are only searched when the hot tier isn't confident.
    tier_min_hot_score: float = 15.0
    tier_min_hot_candidates: int = 3

    # --- Operationalize Production: production hardening / governance monitoring ---
    # How long recipes-audit-log documents are kept before an ISM policy
    # deletes them (see common.opensearch_client.ensure_audit_retention_policy).
    audit_retention_days: int = 180
    # monitor.py's default trailing window and escalation-rate alert threshold.
    governance_alert_lookback_hours: int = 24
    governance_max_escalation_rate: float = 0.5

    # --- Embeddings (kept small on purpose -- see concepts/INFRASTRUCTURE_CHOICES.md) ---
    embedding_model: str = "sentence-transformers/all-MiniLM-L6-v2"
    embedding_dimension: int = 384

    # --- NER service (local, lightweight, spaCy) ---
    ner_service_url: str = "http://127.0.0.1:8000/ner"
    ner_timeout_secs: float = 5.0

    # --- LLM provider selection ---
    # OpenAI-compatible generation is the default. Native Anthropic and Claude
    # through Amazon Bedrock use the same common.llm_client.generate() interface.
    llm_provider: str = "openai"

    # --- Amazon Bedrock ---
    aws_region: str = "us-east-1"
    aws_profile: str = ""
    bedrock_model_id: str = ""
    # Existing names are retained for generation settings shared by all providers.
    # Omit temperature unless explicitly configured: some models reject it.
    bedrock_max_tokens: int = 1024

    # --- OpenAI / OpenAI-compatible ---
    openai_api_key: str = ""
    openai_base_url: str = "https://api.openai.com/v1"
    openai_model: str = ""

    # --- Anthropic API ---
    anthropic_api_key: str = ""
    anthropic_model: str = ""

    # --- RAG behavior ---
    rag_top_k: int = 5
    rag_num_candidates: int = 25

    # --- Recipe HTTP fetch settings (unused unless a pipeline opts into it) ---
    recipe_http_timeout_secs: float = 10.0
    recipe_http_user_agent: str = "rag-retrieval/1.0"


def load_settings() -> Settings:
    """Load settings from environment variables (populated from .env)."""
    _load_env_once()
    _clear_blank_aws_profiles()

    return Settings(
        opensearch_host=_get_str("OPENSEARCH_HOST", Settings.opensearch_host),
        opensearch_port=_get_int("OPENSEARCH_PORT", Settings.opensearch_port),
        opensearch_user=_get_str("OPENSEARCH_USER", Settings.opensearch_user),
        opensearch_password=_get_str("OPENSEARCH_PASSWORD", Settings.opensearch_password),
        opensearch_ssl=_get_bool("OPENSEARCH_SSL", Settings.opensearch_ssl),
        opensearch_hybrid_index=_get_str("OPENSEARCH_HYBRID_INDEX", Settings.opensearch_hybrid_index),
        opensearch_recipe_bm25_index=_get_str("OPENSEARCH_RECIPE_BM25_INDEX", Settings.opensearch_recipe_bm25_index),
        opensearch_recipe_vector_index=_get_str(
            "OPENSEARCH_RECIPE_VECTOR_INDEX", Settings.opensearch_recipe_vector_index
        ),
        opensearch_vector_recipes_baseline_index=_get_str(
            "OPENSEARCH_VECTOR_RECIPES_BASELINE_INDEX", Settings.opensearch_vector_recipes_baseline_index
        ),
        opensearch_bm25_recipes_baseline_index=_get_str(
            "OPENSEARCH_BM25_RECIPES_BASELINE_INDEX", Settings.opensearch_bm25_recipes_baseline_index
        ),
        opensearch_audit_index=_get_str("OPENSEARCH_AUDIT_INDEX", Settings.opensearch_audit_index),
        tier_min_hot_score=_get_float("TIER_MIN_HOT_SCORE", Settings.tier_min_hot_score),
        tier_min_hot_candidates=_get_int("TIER_MIN_HOT_CANDIDATES", Settings.tier_min_hot_candidates),
        audit_retention_days=_get_int("AUDIT_RETENTION_DAYS", Settings.audit_retention_days),
        governance_alert_lookback_hours=_get_int(
            "GOVERNANCE_ALERT_LOOKBACK_HOURS", Settings.governance_alert_lookback_hours
        ),
        governance_max_escalation_rate=_get_float(
            "GOVERNANCE_MAX_ESCALATION_RATE", Settings.governance_max_escalation_rate
        ),
        search_preference=_get_str("SEARCH_PREFERENCE", Settings.search_preference),
        embedding_model=_get_str("EMBEDDING_MODEL", Settings.embedding_model),
        embedding_dimension=_get_int("EMBEDDING_DIMENSION", Settings.embedding_dimension),
        ner_service_url=_get_str("NER_SERVICE_URL", Settings.ner_service_url),
        ner_timeout_secs=_get_float("NER_TIMEOUT_SECS", Settings.ner_timeout_secs),
        llm_provider=_get_str("LLM_PROVIDER", Settings.llm_provider).strip().lower(),
        aws_region=_get_str("AWS_REGION", Settings.aws_region),
        aws_profile=_get_str("AWS_PROFILE", Settings.aws_profile),
        bedrock_model_id=_get_str("BEDROCK_MODEL_ID", Settings.bedrock_model_id),
        bedrock_max_tokens=_get_int("BEDROCK_MAX_TOKENS", Settings.bedrock_max_tokens),
        openai_api_key=_get_str("OPENAI_API_KEY", Settings.openai_api_key),
        openai_base_url=_get_str("OPENAI_BASE_URL", Settings.openai_base_url),
        openai_model=_get_str("OPENAI_MODEL", Settings.openai_model),
        anthropic_api_key=_get_str("ANTHROPIC_API_KEY", Settings.anthropic_api_key),
        anthropic_model=_get_str("ANTHROPIC_MODEL", Settings.anthropic_model),
        rag_top_k=_get_int("RAG_TOP_K", Settings.rag_top_k),
        rag_num_candidates=_get_int("RAG_NUM_CANDIDATES", Settings.rag_num_candidates),
        recipe_http_timeout_secs=_get_float("RECIPE_HTTP_TIMEOUT_SECS", Settings.recipe_http_timeout_secs),
        recipe_http_user_agent=_get_str("RECIPE_HTTP_USER_AGENT", Settings.recipe_http_user_agent),
    )


def require_configured(settings: "Settings") -> None:
    """Fail fast with a clear message instead of a raw connection traceback.

    Every pipeline calls this right after loading settings, so missing values
    produces one actionable error instead of a urllib3/boto3 stack trace.
    """
    problems = []
    if settings.llm_provider == "openai":
        if not settings.openai_api_key:
            problems.append("OPENAI_API_KEY is not set")
        if not settings.openai_model:
            problems.append("OPENAI_MODEL is not set")
    elif settings.llm_provider == "anthropic":
        if not settings.anthropic_api_key:
            problems.append("ANTHROPIC_API_KEY is not set")
        if not settings.anthropic_model:
            problems.append("ANTHROPIC_MODEL is not set")
    elif settings.llm_provider == "bedrock":
        if not settings.bedrock_model_id:
            problems.append("BEDROCK_MODEL_ID is not set")
    else:
        problems.append(
            f"LLM_PROVIDER={settings.llm_provider!r} is not recognized (use 'bedrock' or 'openai')"
        )

    if problems:
        raise RuntimeError(
            "Missing configuration in the environment or optional .env:\n  - "
            + "\n  - ".join(problems)
            + "\n\nUse insta config, export the required settings, or copy support/.env.example "
            "to .env at the lab root and configure OpenSearch and the selected LLM provider."
        )


__all__ = ["Settings", "load_settings", "require_configured", "_get_bool", "_get_int", "_get_float", "_get_str"]
