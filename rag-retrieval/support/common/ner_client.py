# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Client for the local spaCy NER service (see scripts/baseline-bm25-rag/src/ner_service.py)."""
from __future__ import annotations

from typing import Any, Iterable, List, Optional

import requests

from .config import Settings, load_settings
from .labels import normalize_values


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
        """Return True if the service returns a valid entity list for a probe."""
        try:
            self.extract_entities("NER availability check")
            return True
        except RuntimeError:
            return False

    def extract_entities(self, text: str, *, labels: Optional[Iterable[str]] = None) -> List[str]:
        """Return normalized entity strings for ``text``.

        A valid empty entity list is allowed. Failed requests or malformed
        responses stop the operation so incomplete NER work is not reported
        as a successful ingestion or query.
        """
        payload: dict[str, Any] = {"text": str(text or "")}
        label_list = [str(label).strip() for label in (labels or []) if str(label).strip()]
        if label_list:
            payload["labels"] = label_list

        try:
            response = self.session.post(self.service_url, json=payload, timeout=self.timeout)
            response.raise_for_status()
            data = response.json()
        except (requests.RequestException, ValueError) as exc:
            raise RuntimeError(
                "NER request failed; start or restart the NER service and rerun this operation."
            ) from exc

        entities = data.get("entities") if isinstance(data, dict) else None
        if not isinstance(entities, list) or any(not isinstance(value, str) for value in entities):
            raise RuntimeError("NER service returned an invalid 'entities' list; check the service and retry.")
        return normalize_values(entities)


__all__ = ["NERClient"]
