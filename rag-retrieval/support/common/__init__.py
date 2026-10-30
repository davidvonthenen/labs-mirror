# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Shared helpers for every pipeline of the RAG developer journey.

Importing this package also self-heals two common local SSL trust issues so
every pipeline "just works" without a separate manual fix-up step:

1. Python installed via the official python.org .pkg installer does not wire
   itself up to *any* certificate trust store, so HTTPS calls fail with
   ``CERTIFICATE_VERIFY_FAILED: unable to get local issuer certificate``
   even on an unrestricted network.
2. On corporate laptops behind a TLS-inspecting proxy (Zscaler, Netskope,
   etc.), the OS keychain trusts the proxy's re-signed certificates but the
   plain ``certifi`` bundle does not, so the same error can appear even
   after fixing (1). This merges the macOS System keychain's root
   certificates into a local combined bundle so both cases are covered.
"""
import os
import subprocess
import sys
from pathlib import Path

_CACHE_DIR = Path(__file__).resolve().parent.parent.parent / ".cache"
_MERGED_BUNDLE = _CACHE_DIR / "merged-ca-bundle.pem"


def _macos_system_root_pems() -> str:
    """Export root certificates trusted by the macOS System keychain as PEM text.

    Silently returns "" on any failure (non-macOS, no `security` CLI, etc.)
    so this is always a best-effort addition, never a hard dependency.
    """
    if sys.platform != "darwin":
        return ""
    try:
        result = subprocess.run(
            ["security", "find-certificate", "-a", "-p", "/Library/Keychains/System.keychain"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        return result.stdout or ""
    except Exception:
        return ""


def _ensure_merged_cert_bundle() -> str | None:
    """Build (once, cached) a bundle combining certifi + macOS keychain roots."""
    try:
        import certifi
    except ImportError:
        return None

    certifi_path = Path(certifi.where())
    if _MERGED_BUNDLE.exists() and _MERGED_BUNDLE.stat().st_mtime >= certifi_path.stat().st_mtime:
        return str(_MERGED_BUNDLE)

    system_pems = _macos_system_root_pems()
    if not system_pems:
        # Nothing extra to add; certifi alone is the best we can do.
        return str(certifi_path)

    _CACHE_DIR.mkdir(parents=True, exist_ok=True)
    combined = certifi_path.read_text() + "\n" + system_pems
    _MERGED_BUNDLE.write_text(combined)
    return str(_MERGED_BUNDLE)


_bundle = _ensure_merged_cert_bundle()
if _bundle:
    os.environ.setdefault("SSL_CERT_FILE", _bundle)
    os.environ.setdefault("REQUESTS_CA_BUNDLE", _bundle)
    os.environ.setdefault("AWS_CA_BUNDLE", _bundle)

# Some managed machines have a root-owned (unwritable) ~/.cache, which breaks
# the default HuggingFace cache location. Keep the cache project-local instead
# -- it's harmless, gitignored, and sidesteps that permission issue entirely.
_hf_cache = _CACHE_DIR / "huggingface"
_hf_cache.mkdir(parents=True, exist_ok=True)
os.environ.setdefault("HF_HOME", str(_hf_cache))
os.environ.setdefault("HF_HUB_CACHE", str(_hf_cache / "hub"))
