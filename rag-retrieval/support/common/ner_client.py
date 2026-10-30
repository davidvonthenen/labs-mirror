# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Client for the local spaCy NER service (see scripts/baseline-bm25-rag/src/ner_service.py)."""
from __future__ import annotations

from typing import Any, Iterable, List, Optional

import requests

from .config import Settings, load_settings
from .labels import normalize_values
from .logging import get_logger

LOGGER = get_logger(__name__)


class NERClient:
    """Call the local NER Flask service and normalize the returned entities."""

    def __init__(
        self,
        settings: Optional[Settings] = None,
        *,
        service_url: Optional[str] = None,
        timeout: Optional[float] = None,
        session: Optional[requests.Session] = None,
    ) -> None:
        self.settings = settings or load_settings()
        self.service_url = service_url or self.settings.ner_service_url
        self.timeout = float(timeout if timeout is not None else self.settings.ner_timeout_secs)
        self.session = session or requests.Session()

    def is_available(self) -> bool:
        """Return True if the NER service answers a trivial request with HTTP 200 and a JSON object.

        ``extract_entities`` hides service failures (it returns an empty list), so a
        smoke test that must not pass silently without NER calls this first.
        """
        try:
            response = self.session.post(self.service_url, json={"text": "NER availability check"}, timeout=self.timeout)
            return response.status_code == 200 and isinstance(response.json(), dict)
        except Exception:  # noqa: BLE001 -- any failure means "not available"
            return False

    def extract_entities(self, text: str, *, labels: Optional[Iterable[str]] = None) -> List[str]:
        """Return normalized entity strings for ``text``.

        Network/service failures are swallowed into an empty list so a
        flaky NER call never takes down ingest or query -- retrieval simply
        falls back to full-text matching for that one item.
        """
        payload: dict[str, Any] = {"text": str(text or "")}
        label_list = [str(label).strip() for label in (labels or []) if str(label).strip()]
        if label_list:
            payload["labels"] = label_list

        try:
            response = self.session.post(self.service_url, json=payload, timeout=self.timeout)
            response.raise_for_status()
            data = response.json()
        except Exception as exc:
            LOGGER.warning("NER request failed for %d chars: %s", len(payload["text"]), exc)
            return []

        entities = data.get("entities") if isinstance(data, dict) else []
        if not isinstance(entities, list):
            return []
        return normalize_values(entities)


__all__ = ["NERClient"]
