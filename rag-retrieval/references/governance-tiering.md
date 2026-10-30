[Task 6: Governance Tiering](../tasks/governance-tiering.md)

# Governance Tiering reference and source map

## Commands

Run from `scripts/governance-tiering/` with the lab's `.venv` active and Baseline BM25 RAG's NER service running.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make ingest` | `python -m src.ingest` | Re-indexes `recipes-bm25` / `recipes-vector` with `assign_tier()` and creates `recipes-audit-log`; logs the hot/lt distribution. |
| `make query QUESTION="..." ARGS="..."` | `python -m src.query --question "..."` | Hot-tier grounding, optional escalation, vector refinement, cited answer, and one audit record. Prints `GOVERNANCE:` and `CITATIONS:` lines. |
| `make validate` | `python -m src.validate` | Smoke test; prints four `[PASS]` lines. |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

`escalation_reason` is `no_hot_candidates`, `named_source_not_in_hot_tier`, or `low_confidence_hot_score` (which also covers too few hot candidates), checked in that order, or `None` without escalation. Thresholds come from `TIER_MIN_HOT_SCORE` (default 15.0) and `TIER_MIN_HOT_CANDIDATES` (default 3).

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/governance-tiering/Makefile) | Install, ingest, query, validate, and clean targets. |
| [`src/__init__.py`](../scripts/governance-tiering/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/ingest.py`](../scripts/governance-tiering/src/ingest.py) | `assign_tier()` (the governance rule), `build_text()`, `_shared_fields()`, and `main()`; also calls `ensure_audit_index()`. |
| [`src/query.py`](../scripts/governance-tiering/src/query.py) | `ask()` returns `(answer, grounding_hits, refined_hits, governance_meta)`. Helpers: `_extract_domain_tokens()`, `_assign_handles()`, `_build_context()`, `_build_system_prompt()`, `_extract_cited_handles()`; `REFINE_ANN_POOL = 300`; writes the audit record with `log_audit_event()`. |
| [`src/validate.py`](../scripts/governance-tiering/src/validate.py) | Citation-extraction and source-detection tests (no live services), hot-tier no-escalation test, and LT-tier escalation test. The two live tests fail if the NER service is down. |
| [`support/recipes/recipes_sample.json`](../support/recipes/recipes_sample.json) | The curated 260-recipe sample (209 hot, 51 lt under `assign_tier()`). Shared by every task's `ingest.py`; the path is `RECIPES_SAMPLE_PATH` in `support/common/config.py`. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `ensure_audit_index()`, `log_audit_event()`, and the Hybrid RAG (NER + Rerank) query builders with the `tier` filter. |
| [`support/common/config.py`](../support/common/config.py) | `tier_min_hot_score`, `tier_min_hot_candidates`, `opensearch_audit_index`. |
| [Choosing a retrieval approach](../concepts/CHOOSING_RETRIEVAL.md) and [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md) | Where governance fits among the approaches, and what the entity step does. |
| [Troubleshooting](../troubleshooting/governance-tiering.md) | Setup and runtime failures for this task. |
