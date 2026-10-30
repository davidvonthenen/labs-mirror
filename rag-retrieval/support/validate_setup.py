# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Check that the workspace is ready before starting Task 2.

Run it from the lab root (the ``rag-retrieval/`` folder):

    python support/validate_setup.py                    # offline checks only
    python support/validate_setup.py --check-opensearch # also contact the cluster (read-only)
    python support/validate_setup.py --check-embeddings # also load the embedding model
    python support/validate_setup.py --check-ner        # also call the local NER service

The default checks never use the network (the AWS credential check reads local
sources only, with the EC2 metadata lookup disabled). The script never prints
the value of any setting or credential; it reports only whether a setting is
present, which credential source was found, and the non-secret index names the
tasks will use.

A [WARN] line does not fail the run. Exit code 0 means no check failed; 1 means
at least one check failed.
"""
from __future__ import annotations

import argparse
import importlib.util
import os
import sys
from pathlib import Path
from typing import Callable, List, Tuple

SUPPORT_DIR = Path(__file__).resolve().parent
LAB_ROOT = SUPPORT_DIR.parent
MIN_PYTHON = (3, 10)
RECOMMENDED_PYTHON = (3, 12)  # default compatibility-test target

TASK_DIRS = (
    "baseline-vector-rag",
    "baseline-bm25-rag",
    "hybrid-rag-native",
    "hybrid-rag-ner-rerank",
    "governance-tiering",
    "operationalize-production",
)

# import name -> pip package name (only where they differ, for the hint)
REQUIRED_MODULES = {
    "opensearchpy": "opensearch-py",
    "bm25s": "bm25s",
    "Stemmer": "PyStemmer",
    "flask": "flask",
    "dotenv": "python-dotenv",
    "requests": "requests",
    "openai": "openai",
    "boto3": "boto3",
    "sentence_transformers": "sentence-transformers",
    "tqdm": "tqdm",
    "numpy": "numpy",
    "spacy": "spacy",
}
SPACY_MODEL = "en_core_web_sm"

Result = Tuple[bool, str]


def check_python() -> Result:
    found = sys.version_info[:3]
    text = ".".join(str(p) for p in found)
    recommended = ".".join(str(p) for p in RECOMMENDED_PYTHON)
    if found < MIN_PYTHON + (0,) or found >= (3, 15, 0):
        return False, f"Python {text} found; Python 3.10-3.14 is supported (3.12 recommended)"
    if found[:2] == RECOMMENDED_PYTHON:
        return True, f"Python {text} (recommended version)"
    return True, f"Python {text}; Python {recommended} is the default recommended version"


def check_layout() -> Result:
    missing: List[str] = []
    for rel in ("README.md", "architecture.md", "support/requirements.txt", "support/.env.example",
                "support/recipes/recipes_sample.json"):
        if not (LAB_ROOT / rel).is_file():
            missing.append(rel)
    for task in TASK_DIRS:
        base = LAB_ROOT / "scripts" / task
        for rel in ("Makefile", "src/__init__.py", "src/common/config.py"):
            if not (base / rel).is_file():
                missing.append(f"scripts/{task}/{rel}")
    if missing:
        return False, "missing: " + ", ".join(missing)
    return True, f"README, architecture, support and {len(TASK_DIRS)} task folders present"


def check_dependencies() -> Result:
    missing = [pip for mod, pip in REQUIRED_MODULES.items() if importlib.util.find_spec(mod) is None]
    if missing:
        return False, "not installed: " + ", ".join(missing) + " (run: pip install -r support/requirements.txt)"
    return True, f"{len(REQUIRED_MODULES)} required packages importable"


def check_spacy_model() -> Result:
    if importlib.util.find_spec("spacy") is None:
        return False, "spaCy is not installed, so the model cannot be checked"
    # Match the model selected by the NER service when configured in the shell.
    model_name = os.getenv("SPACY_MODEL", SPACY_MODEL)
    hint = f"run: python -m spacy download {model_name}"
    try:
        import spacy

        if not spacy.util.is_package(model_name):
            return False, f"spaCy model {model_name} missing ({hint})"
        model_path = spacy.util.get_package_path(model_name)
        metadata = spacy.util.get_model_meta(model_path)
        required_version = metadata.get("spacy_version")
        if required_version and not spacy.util.is_compatible_version(spacy.__version__, required_version):
            return False, f"spaCy model {model_name} is incompatible with spaCy {spacy.__version__} ({hint})"
        nlp = spacy.load(model_name)
        if "ner" not in nlp.pipe_names:
            return False, f"spaCy model {model_name} does not contain an NER component"
    except Exception as exc:  # report the error class, not environment values
        return False, f"spaCy model {model_name} could not load ({type(exc).__name__}; {hint})"
    return True, f"spaCy {spacy.__version__}; model {model_name} loaded"


def _settings():
    sys.path.insert(0, str(SUPPORT_DIR))
    from common.config import load_settings  # noqa: WPS433 (deliberate late import)

    return load_settings()


def check_env_file() -> Result:
    # Files are optional: the current shell has precedence in common.config.
    if (Path.cwd() / ".env").is_file():
        where = "at the lab root" if Path.cwd().resolve() == LAB_ROOT else "in the current directory"
        return True, f".env found {where}"
    if (LAB_ROOT / ".env").is_file():
        return True, ".env found at the lab root"
    if (SUPPORT_DIR / ".env").is_file():
        return True, ".env found in support/"
    if os.getenv("LAB_CONTAINER", "").strip().lower() in ("1", "true", "yes", "on"):
        # Compose injects support/.env at runtime. It must not be baked into the
        # image or copied into the persistent workspace. Settings are checked next.
        return True, "runtime environment mode; required settings are checked separately"
    return False, ".env not found (copy support/.env.example to .env at the lab root and fill it in)"


def check_configuration() -> Result:
    if importlib.util.find_spec("dotenv") is None:
        return False, "python-dotenv is not installed, so configuration cannot be loaded"
    settings = _settings()
    from common.config import require_configured  # noqa: WPS433
    try:
        require_configured(settings)
    except RuntimeError as exc:
        # The message names missing settings only; it never contains values.
        first = str(exc).split("\n\n")[0].replace("Missing configuration in .env:\n  - ", "")
        return False, "missing or unrecognized: " + first.replace("\n  - ", "; ")
    return True, "required OpenSearch and LLM settings are present"


def check_aws_credentials() -> Result:
    """Look for AWS credentials without calling AWS. Reports the source, never a value."""
    import os

    if importlib.util.find_spec("boto3") is None:
        return False, "boto3 is not installed, so credentials cannot be checked"
    import boto3

    previous = os.environ.get("AWS_EC2_METADATA_DISABLED")
    os.environ["AWS_EC2_METADATA_DISABLED"] = "true"  # no network lookup of instance credentials
    try:
        credentials = boto3.session.Session().get_credentials()
    except Exception as exc:  # noqa: BLE001
        return False, f"could not resolve AWS credentials ({type(exc).__name__}); check your AWS profile or SSO login"
    finally:
        if previous is None:
            os.environ.pop("AWS_EC2_METADATA_DISABLED", None)
        else:
            os.environ["AWS_EC2_METADATA_DISABLED"] = previous
    if credentials is None:
        return False, (
            "no AWS credentials found; set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY in .env, "
            "or configure an AWS profile or SSO login (or set LLM_PROVIDER=openai)"
        )
    method = getattr(credentials, "method", "unknown")
    return True, f"AWS credentials found (source: {method}); Bedrock model access is not checked here"


def check_index_names() -> Result:
    settings = _settings()
    names = {
        "OPENSEARCH_VECTOR_RECIPES_BASELINE_INDEX": settings.opensearch_vector_recipes_baseline_index,
        "OPENSEARCH_BM25_RECIPES_BASELINE_INDEX": settings.opensearch_bm25_recipes_baseline_index,
        "OPENSEARCH_HYBRID_INDEX": settings.opensearch_hybrid_index,
        "OPENSEARCH_RECIPE_BM25_INDEX": settings.opensearch_recipe_bm25_index,
        "OPENSEARCH_RECIPE_VECTOR_INDEX": settings.opensearch_recipe_vector_index,
        "OPENSEARCH_AUDIT_INDEX": settings.opensearch_audit_index,
    }
    return True, "index names in use: " + ", ".join(f"{k}={v}" for k, v in names.items())


def check_opensearch() -> Result:
    import logging

    from common.opensearch_client import create_client  # noqa: WPS433

    # Keep library tracebacks and the connection log line (it names the host) out of the report.
    for noisy in ("opensearch", "urllib3", "common.opensearch_client"):
        logging.getLogger(noisy).setLevel(logging.CRITICAL)
    try:
        client = create_client(_settings())
        info = client.info()
    except Exception as exc:  # noqa: BLE001 - report the class only, never the message
        return False, f"could not reach the cluster ({type(exc).__name__}); check host, port, credentials and IP allow-list"
    version = info.get("version", {}).get("number", "unknown")
    detail = f"cluster reachable, OpenSearch {version}"

    # Optional extras the tasks rely on; a failure to list plugins is not a failure of the check.
    try:
        rows = client.transport.perform_request("GET", "/_cat/plugins?format=json")
        components = {str(row.get("component", "")) for row in rows if isinstance(row, dict)}
    except Exception:  # noqa: BLE001
        return True, detail + "; plugin list unavailable (the API may be restricted for this user)"
    has_neural = any("neural-search" in name for name in components)
    has_ism = any("index-management" in name for name in components)
    detail += f"; neural-search plugin: {'yes' if has_neural else 'NO'}; index-management (ISM) plugin: {'yes' if has_ism else 'no'}"
    if not has_neural:
        return True, "WARN: " + detail + " (Hybrid RAG (Native) needs neural-search)"
    return True, detail


def check_embeddings() -> Result:
    from common.embeddings import EmbeddingModel  # noqa: WPS433

    settings = _settings()
    try:
        model = EmbeddingModel(settings)
        vector = model.encode(["setup check"])[0]
    except Exception as exc:  # noqa: BLE001
        return False, f"could not load or run the embedding model ({type(exc).__name__})"
    if len(vector) != settings.embedding_dimension:
        return False, f"model returned {len(vector)} dimensions; EMBEDDING_DIMENSION is {settings.embedding_dimension}"
    return True, f"{settings.embedding_model} loaded, {len(vector)} dimensions"


def check_ner() -> Result:
    import requests

    settings = _settings()
    try:
        response = requests.post(settings.ner_service_url, json={"text": "Apple opened a store in London."}, timeout=settings.ner_timeout_secs)
        response.raise_for_status()
        entities = response.json().get("entities")
    except Exception as exc:  # noqa: BLE001
        return False, (
            f"NER service did not answer ({type(exc).__name__}); start it with "
            "`python -m src.ner_service` in scripts/baseline-bm25-rag"
        )
    if not isinstance(entities, list):
        return False, "NER service answered but the response has no 'entities' list"
    return True, f"NER service answered ({len(entities)} entities for a test sentence)"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--check-opensearch", action="store_true", help="also contact the cluster (read-only)")
    parser.add_argument("--check-embeddings", action="store_true", help="also load the embedding model")
    parser.add_argument("--check-ner", action="store_true", help="also call the local NER service (start it first)")
    args = parser.parse_args()

    checks: List[Tuple[str, Callable[[], Result]]] = [
        ("python", check_python),
        ("layout", check_layout),
        ("dependencies", check_dependencies),
        ("spacy-model", check_spacy_model),
        ("env-file", check_env_file),
    ]
    env_ok = True
    results: List[bool] = []

    def run(name: str, fn: Callable[[], Result]) -> bool:
        ok, detail = fn()
        if ok and detail.startswith("WARN: "):
            print(f"[WARN] {name}: {detail[len('WARN: '):]}")
        else:
            print(f"[{'PASS' if ok else 'FAIL'}] {name}: {detail}")
        return ok

    for name, fn in checks:
        results.append(run(name, fn))
    env_ok = results[2] and results[4]  # dependencies + env file

    if env_ok:
        config_ok = run("configuration", check_configuration)
        results.append(config_ok)
        if config_ok:
            results.append(run("index-names", check_index_names))
            if _settings().llm_provider == "bedrock":
                results.append(run("aws-credentials", check_aws_credentials))
            if args.check_opensearch:
                results.append(run("opensearch", check_opensearch))
            if args.check_embeddings:
                results.append(run("embeddings", check_embeddings))
            if args.check_ner:
                results.append(run("ner", check_ner))
    else:
        print("[SKIP] configuration: needs the dependencies and a .env file first")
        results.append(False)
        if args.check_opensearch:
            print("[SKIP] opensearch: needs a valid configuration first")
        if args.check_embeddings:
            print("[SKIP] embeddings: needs a valid configuration first")
        if args.check_ner:
            print("[SKIP] ner: needs a valid configuration first")

    if all(results):
        print("Setup check passed.")
        return 0
    print("Setup check failed. Fix the FAIL lines above (see troubleshooting/setup-environment.md).")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
