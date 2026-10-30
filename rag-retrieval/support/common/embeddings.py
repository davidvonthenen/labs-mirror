# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Embedding utilities using sentence-transformers.

Deliberately uses a small model (``all-MiniLM-L6-v2``, ~90MB, 384 dims).
That keeps every pipeline runnable on modest hardware (see
concepts/INFRASTRUCTURE_CHOICES.md's memory budget)
without any macOS/GPU-specific tuning.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from functools import lru_cache
from typing import Iterable, List, Optional, Sequence, Union

import numpy as np
from sentence_transformers import SentenceTransformer

from .config import Settings
from .logging import get_logger

LOGGER = get_logger(__name__)


@lru_cache(maxsize=2)
def _load_model(model_name: str) -> SentenceTransformer:
    LOGGER.info("Loading embedding model '%s' (CPU-friendly, cached per process)", model_name)
    return SentenceTransformer(model_name)


@dataclass
class EmbeddingModel:
    """Thin wrapper around a cached sentence-transformers model."""

    settings: Settings
    _cached_model: Optional[SentenceTransformer] = field(default=None, repr=False)

    @property
    def model_name(self) -> str:
        return self.settings.embedding_model

    @property
    def model(self) -> SentenceTransformer:
        if self._cached_model is None:
            self._cached_model = _load_model(self.model_name)
        return self._cached_model

    @property
    def dimension(self) -> int:
        for method_name in ("get_embedding_dimension", "get_sentence_embedding_dimension"):
            method = getattr(self.model, method_name, None)
            if method is not None:
                try:
                    return int(method())
                except Exception:
                    continue
        return int(self.settings.embedding_dimension)

    def encode(self, texts: Iterable[str]) -> List[np.ndarray]:
        """Encode texts into L2-normalized vectors (cosine similarity ready)."""
        items: List[str] = list(texts)
        if not items:
            return []
        arr = self.model.encode(
            items,
            convert_to_numpy=True,
            normalize_embeddings=True,
            show_progress_bar=False,
        )
        if isinstance(arr, np.ndarray) and arr.ndim == 2:
            return [arr[i] for i in range(arr.shape[0])]
        if isinstance(arr, np.ndarray) and arr.ndim == 1:
            return [arr]
        return [np.asarray(v, dtype=float) for v in arr]


def to_list(vec: Union[np.ndarray, Sequence[float], List[float]]) -> List[float]:
    """Convert a vector-like object into a plain list of floats for OpenSearch."""
    if isinstance(vec, np.ndarray):
        return vec.astype(float).tolist()
    return [float(x) for x in vec]


__all__ = ["EmbeddingModel", "to_list"]
