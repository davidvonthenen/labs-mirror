# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Shared logging setup for every pipeline."""
from __future__ import annotations

import logging
import os

_CONFIGURED = False


def _configure() -> None:
    global _CONFIGURED
    if _CONFIGURED:
        return
    level = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=level,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%H:%M:%S",
    )
    # Third-party HTTP libraries are extremely chatty at INFO; keep their
    # noise out unless the user explicitly asks for DEBUG.
    if level != "DEBUG":
        for noisy in ("httpx", "httpcore", "urllib3", "sentence_transformers", "huggingface_hub"):
            logging.getLogger(noisy).setLevel(logging.WARNING)
    _CONFIGURED = True


def get_logger(name: str) -> logging.Logger:
    _configure()
    return logging.getLogger(name)


__all__ = ["get_logger"]
