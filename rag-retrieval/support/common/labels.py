# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Label normalization helpers for OpenSearch keyword metadata.

Recipe metadata (allergen cautions, diet labels, health labels, etc.) is
stored both in its original display form and in a normalized lowercase form.
Normalized values are what filters/exclusions match against, so "Sulfites",
"sulfites", and " Sulfites " all resolve to the same filter term.
"""
from __future__ import annotations

import re
from typing import Any, Iterable, List

_SPACE_RE = re.compile(r"\s+")


def normalize_key(value: Any) -> str:
    """Normalize a human label for deterministic keyword filtering."""
    text = str(value or "").strip().lower()
    return _SPACE_RE.sub(" ", text)


def normalize_values(values: Iterable[Any]) -> List[str]:
    """Normalize and de-duplicate a sequence of labels while preserving order."""
    out: List[str] = []
    seen: set[str] = set()
    for value in values or []:
        key = normalize_key(value)
        if not key or key in seen:
            continue
        seen.add(key)
        out.append(key)
    return out


__all__ = ["normalize_key", "normalize_values"]
