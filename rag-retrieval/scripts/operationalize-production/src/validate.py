#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Pass/fail smoke test for Operationalize Production. Run after Governance Tiering has written at least
one record to `recipes-audit-log` (see
tasks/operationalize-production.md)."""
from __future__ import annotations

import json
import tempfile
from pathlib import Path

from .common.config import load_settings, require_configured
from .common.opensearch_client import (
    audit_log_governance_stats,
    create_client,
    ensure_audit_index,
    ensure_audit_retention_policy,
)
from .monitor import evaluate_alerts
from .policy_registry import check_drift, load_history


def test_operationalize_production_evaluate_alerts_flags_hallucination_and_escalation_drift() -> None:
    """Deterministic unit test for the alert-decision logic itself, so this
    doesn't depend on live audit data happening to contain a real alert."""
    clean_stats = {
        "total": 10,
        "escalated": 2,
        "escalation_rate": 0.2,
        "escalation_reasons": {"low_confidence_hot_score": 2},
        "citations_invalid": 0,
        "hallucination_rate": 0.0,
    }
    healthy_ner = {"healthy": True, "status_code": 200}
    assert evaluate_alerts(clean_stats, healthy_ner, max_escalation_rate=0.5) == []

    hallucinating_stats = {**clean_stats, "citations_invalid": 1, "hallucination_rate": 0.1}
    alerts = evaluate_alerts(hallucinating_stats, healthy_ner, max_escalation_rate=0.5)
    assert len(alerts) == 1 and "hallucinated citation" in alerts[0], alerts

    drifted_stats = {**clean_stats, "escalated": 8, "escalation_rate": 0.8}
    alerts2 = evaluate_alerts(drifted_stats, healthy_ner, max_escalation_rate=0.5)
    assert len(alerts2) == 1 and "escalation rate" in alerts2[0], alerts2

    unhealthy_ner = {"healthy": False, "error": "Connection refused"}
    alerts3 = evaluate_alerts(clean_stats, unhealthy_ner, max_escalation_rate=0.5)
    assert len(alerts3) == 1 and "NER service" in alerts3[0], alerts3

    empty_stats = {"total": 0}
    assert evaluate_alerts(empty_stats, healthy_ner, max_escalation_rate=0.5) == []
    print("[PASS] Operationalize Production evaluate_alerts() correctly flags hallucination/escalation/NER-health issues")


def test_operationalize_production_policy_registry_detects_in_sync_and_drifted() -> None:
    """Uses temporary history files so this doesn't depend on -- or
    mutate -- the real checked-in policy_history.json."""
    settings = load_settings()

    with tempfile.TemporaryDirectory() as tmpdir:
        in_sync_path = Path(tmpdir) / "in_sync.json"
        in_sync_path.write_text(
            json.dumps(
                {
                    "history": [
                        {
                            "version": 1,
                            "date": "2026-01-01",
                            "approved_by": "test",
                            "tier_min_hot_score": settings.tier_min_hot_score,
                            "tier_min_hot_candidates": settings.tier_min_hot_candidates,
                        }
                    ]
                }
            )
        )
        result = check_drift(in_sync_path)
        assert result["in_sync"], result
        assert not result["drifted"], result

        drifted_path = Path(tmpdir) / "drifted.json"
        drifted_path.write_text(
            json.dumps(
                {
                    "history": [
                        {
                            "version": 1,
                            "date": "2026-01-01",
                            "approved_by": "test",
                            "tier_min_hot_score": settings.tier_min_hot_score + 100.0,
                            "tier_min_hot_candidates": settings.tier_min_hot_candidates,
                        }
                    ]
                }
            )
        )
        result2 = check_drift(drifted_path)
        assert not result2["in_sync"], result2
        assert "tier_min_hot_score" in result2["drifted"], result2

    # The real, checked-in history file should also load and parse cleanly.
    real_history = load_history()
    assert real_history.get("history"), "policy_history.json should have at least one recorded version"
    print("[PASS] Operationalize Production policy_registry correctly detects in-sync vs. drifted thresholds")


def test_operationalize_production_audit_retention_policy_can_be_ensured() -> None:
    """Live-cluster test: attaching the ISM retention policy must be
    idempotent and must not raise, even if it already exists from a
    previous run. Reports (rather than fails) if ISM is unavailable on this
    cluster, since that's a real possibility on some managed tiers -- see
    tasks/operationalize-production.md section A."""
    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    client = create_client(settings)
    ensure_audit_index(client, settings.opensearch_audit_index)

    ok = ensure_audit_retention_policy(
        client, settings.opensearch_audit_index, retention_days=settings.audit_retention_days
    )
    ok_again = ensure_audit_retention_policy(
        client, settings.opensearch_audit_index, retention_days=settings.audit_retention_days
    )
    assert ok == ok_again, "ensure_audit_retention_policy() should be idempotent"
    if ok:
        print(
            f"[PASS] Operationalize Production ISM retention policy ensured on '{settings.opensearch_audit_index}' "
            f"({settings.audit_retention_days}d)"
        )
    else:
        print(
            "[SKIP] Operationalize Production ISM retention policy could not be applied -- ISM plugin appears unavailable "
            "on this cluster. Flag for manual review per tasks/operationalize-production.md section A."
        )


def test_operationalize_production_live_governance_stats_are_well_formed() -> None:
    """Live-cluster test: whatever real audit data Governance Tiering has already
    written, the aggregation must return well-formed rates."""
    settings = load_settings()
    require_configured(settings, need_bedrock=False)
    client = create_client(settings)
    ensure_audit_index(client, settings.opensearch_audit_index)

    stats = audit_log_governance_stats(client, settings.opensearch_audit_index, lookback_hours=24 * 365)
    assert "error" not in stats, stats
    assert isinstance(stats["total"], int) and stats["total"] >= 0, stats
    assert 0.0 <= stats["escalation_rate"] <= 1.0, stats
    assert 0.0 <= stats["hallucination_rate"] <= 1.0, stats
    print(f"[PASS] Operationalize Production audit_log_governance_stats() returned well-formed stats: {stats}")


if __name__ == "__main__":
    test_operationalize_production_evaluate_alerts_flags_hallucination_and_escalation_drift()
    test_operationalize_production_policy_registry_detects_in_sync_and_drifted()
    test_operationalize_production_audit_retention_policy_can_be_ensured()
    test_operationalize_production_live_governance_stats_are_well_formed()
