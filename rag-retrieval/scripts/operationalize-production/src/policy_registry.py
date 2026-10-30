#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Operationalize Production: change control for the hot/LT tiering governance
policy.

Before this task, `assign_tier()`'s rule (Governance Tiering) and the
`TIER_MIN_HOT_SCORE` / `TIER_MIN_HOT_CANDIDATES` thresholds it depends on
lived only in code and `.env`, with no change history -- even though they
define what counts as "safety-critical" data. `policy_history.json` is the
checked-in record of every approved version; this script checks the
CURRENTLY CONFIGURED thresholds against the latest approved entry and fails
loudly if they've drifted without a matching history entry being added.

This deliberately does NOT catch changes to `assign_tier()`'s qualitative
rule itself (e.g. someone changing which field counts as "safety-critical")
-- only the numeric thresholds are machine-checkable from `.env` alone. A
code-review requirement on
`scripts/governance-tiering/src/ingest.py`'s `assign_tier()` is
the other half of this control; see tasks/operationalize-production.md.
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Dict, Sequence

from .common.config import load_settings
from .common.logging import get_logger

LOGGER = get_logger(__name__)

HISTORY_PATH = Path(__file__).parent / "policy_history.json"


def load_history(path: Path = HISTORY_PATH) -> Dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def latest_entry(history: Dict[str, Any]) -> Dict[str, Any]:
    entries = history.get("history") or []
    if not entries:
        raise ValueError(f"{HISTORY_PATH} has no recorded policy versions")
    return max(entries, key=lambda entry: entry["version"])


def check_drift(history_path: Path = HISTORY_PATH) -> Dict[str, Any]:
    """Compare the currently configured thresholds against the latest
    approved `policy_history.json` entry. Returns a result dict; raises
    nothing itself -- callers (main()/validate.py) decide how to react."""
    settings = load_settings()
    history = load_history(history_path)
    latest = latest_entry(history)

    drifted: Dict[str, Dict[str, Any]] = {}
    if settings.tier_min_hot_score != latest["tier_min_hot_score"]:
        drifted["tier_min_hot_score"] = {
            "configured": settings.tier_min_hot_score,
            "approved": latest["tier_min_hot_score"],
        }
    if settings.tier_min_hot_candidates != latest["tier_min_hot_candidates"]:
        drifted["tier_min_hot_candidates"] = {
            "configured": settings.tier_min_hot_candidates,
            "approved": latest["tier_min_hot_candidates"],
        }

    return {
        "approved_version": latest["version"],
        "approved_date": latest.get("date"),
        "approved_by": latest.get("approved_by"),
        "drifted": drifted,
        "in_sync": not drifted,
    }


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Check .env tiering thresholds against the approved policy history")
    parser.add_argument("--history-file", type=str, default=str(HISTORY_PATH))
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    result = check_drift(Path(args.history_file))

    print(
        f"POLICY REGISTRY: latest approved version={result['approved_version']} "
        f"({result['approved_date']}, approved_by={result['approved_by']!r})"
    )
    if result["in_sync"]:
        print("[OK] Configured TIER_MIN_HOT_SCORE / TIER_MIN_HOT_CANDIDATES match the latest approved version.")
        return 0

    print("[DRIFT DETECTED] Configured thresholds no longer match the latest approved policy version:")
    for field, values in result["drifted"].items():
        print(f"  - {field}: configured={values['configured']!r} approved={values['approved']!r}")
    print(
        f"\nIf this change was intentional, add a new entry to {args.history_file} recording the new "
        "values, date, and who approved it -- then re-run this check."
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
