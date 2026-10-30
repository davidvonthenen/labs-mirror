#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Operationalize Production: governance-signal monitoring over `recipes-audit-log`.

Governance Tiering made sure every query *writes* a governance audit record. Nothing
before this task ever *reads* them back proactively. This is the
automatable half of the audit-log work -- run it on a schedule (cron / CI) against
the same audit index every pipeline writes to, and it alerts (non-zero exit
code, plus printed detail) if:

  - Any query's answer contained a hallucinated citation
    (`citations_valid=False`). This should never happen silently -- the
    RESPONSE POLICY (block the answer? auto-retry? queue for human review?)
    is a governance decision this task surfaces but deliberately does not
    make for you. Right now a hallucinated citation is only ever recorded,
    never acted on; that's the gap this alert exists to close.
  - The escalation rate over the lookback window exceeds a configured
    threshold (`GOVERNANCE_MAX_ESCALATION_RATE`) -- a leading indicator of
    hot/LT tiering drift, e.g. newly-ingested "hot" data missing a caution
    it should have, or a change that silently shifted the "hot" fraction.
  - The local NER service (`ner_service.py`) isn't responding. The grounding
    step of Baseline BM25 RAG, Hybrid RAG (NER + Rerank) and Governance
    Tiering lose their entity boost (and Baseline BM25 RAG / ingest lose the
    NER tags) when NER is down; hard filters such as --exclude and --require
    still apply. That silently degrades ranking rather than failing loudly --
    this makes that degradation visible.
"""
from __future__ import annotations

import argparse
import sys
from typing import Any, Dict, List, Sequence

import requests

from .common.config import Settings, load_settings, require_configured
from .common.logging import get_logger
from .common.opensearch_client import (
    audit_log_governance_stats,
    create_client,
    fetch_hallucinated_audit_samples,
)

LOGGER = get_logger(__name__)


def check_ner_service_health(settings: Settings) -> Dict[str, Any]:
    """Best-effort health probe. `ner_service.py` has no dedicated /health
    route, so this reuses the real /ner endpoint with a trivial payload --
    a 200 with the expected JSON shape means the service is actually
    answering requests, not just accepting TCP connections."""
    try:
        response = requests.post(
            settings.ner_service_url, json={"text": "governance monitor health check"}, timeout=settings.ner_timeout_secs
        )
        healthy = response.status_code == 200 and isinstance(response.json(), dict)
        return {"healthy": healthy, "status_code": response.status_code}
    except Exception as exc:  # noqa: BLE001 -- any failure means "not healthy"
        return {"healthy": False, "error": str(exc)}


def evaluate_alerts(stats: Dict[str, Any], ner_health: Dict[str, Any], *, max_escalation_rate: float) -> List[str]:
    """Pure decision logic, factored out so it's unit-testable without a
    live cluster or a running NER service (see validate.py)."""
    alerts: List[str] = []

    if stats.get("total"):
        if stats.get("citations_invalid", 0) > 0:
            alerts.append(
                f"{stats['citations_invalid']} of {stats['total']} answer(s) contained a hallucinated "
                "citation (citations_valid=False) -- see samples above"
            )
        if stats.get("escalation_rate", 0.0) > max_escalation_rate:
            alerts.append(
                f"escalation rate {stats['escalation_rate']:.1%} exceeds threshold {max_escalation_rate:.1%} "
                "-- possible hot/LT tiering drift (check for newly-ingested hot-tier data missing a "
                "required caution)"
            )

    if not ner_health.get("healthy"):
        detail = ner_health.get("error") or f"HTTP {ner_health.get('status_code')}"
        alerts.append(
            f"NER service is not responding ({detail}) -- entity boosting is silently disabled, so "
            "ranking quality degrades (hard filters still apply)"
        )

    return alerts


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Check recipes-audit-log for governance alerts")
    parser.add_argument("--audit-index-name", type=str, default=None)
    parser.add_argument("--lookback-hours", type=int, default=None)
    parser.add_argument("--max-escalation-rate", type=float, default=None)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    settings = load_settings()
    require_configured(settings, need_bedrock=False)

    audit_index = args.audit_index_name or settings.opensearch_audit_index
    lookback_hours = args.lookback_hours if args.lookback_hours is not None else settings.governance_alert_lookback_hours
    max_escalation_rate = (
        args.max_escalation_rate if args.max_escalation_rate is not None else settings.governance_max_escalation_rate
    )

    client = create_client(settings)
    stats = audit_log_governance_stats(client, audit_index, lookback_hours=lookback_hours)

    if not stats["total"]:
        print(f"GOVERNANCE MONITOR: no audit records in the last {lookback_hours}h in '{audit_index}'.")
    else:
        print(
            f"GOVERNANCE MONITOR: {stats['total']} queries in the last {lookback_hours}h -- "
            f"escalation_rate={stats['escalation_rate']:.1%} hallucination_rate={stats['hallucination_rate']:.1%}"
        )
        if stats["escalation_reasons"]:
            print(f"  escalation_reasons={stats['escalation_reasons']}")
        if stats["citations_invalid"]:
            samples = fetch_hallucinated_audit_samples(client, audit_index, lookback_hours=lookback_hours)
            for sample in samples:
                print(
                    f"  [HALLUCINATION] {sample.get('timestamp')} question={sample.get('question')!r} "
                    f"hallucinated={sample.get('hallucinated_citations')}"
                )

    ner_health = check_ner_service_health(settings)
    if ner_health.get("healthy"):
        print(f"GOVERNANCE MONITOR: NER service at {settings.ner_service_url} is healthy.")

    alerts = evaluate_alerts(stats, ner_health, max_escalation_rate=max_escalation_rate)
    if alerts:
        print("\nALERTS:")
        for alert in alerts:
            print(f"  - {alert}")
        return 1

    print("\nNo governance alerts.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
