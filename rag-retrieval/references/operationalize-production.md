[Task 7: Operationalize Production](../tasks/operationalize-production.md)

# Operationalize Production reference and source map

## Commands

Run from `scripts/operationalize-production/` with the lab's `.venv` active. `monitor` probes Baseline BM25 RAG's NER service and reads the audit records Governance Tiering writes.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make monitor ARGS="--lookback-hours 72"` | `python -m src.monitor --lookback-hours 72` | Prints governance stats and NER health; exits `1` on any alert, `0` with `No governance alerts.` |
| `make policy` | `python -m src.policy_registry` | Compares `.env` thresholds with the latest `policy_history.json` entry; prints `[OK]` and exits `0`, or `[DRIFT DETECTED]` and exits `1`. |
| `make validate` | `python -m src.validate` | Smoke test; two offline checks, then the ISM retention check (`[PASS]` or `[SKIP]`) and live governance stats. |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

`monitor` options: `--audit-index-name`, `--lookback-hours` (default `GOVERNANCE_ALERT_LOOKBACK_HOURS`, 24), `--max-escalation-rate` (default `GOVERNANCE_MAX_ESCALATION_RATE`, 0.5). `policy` option: `--history-file`.

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/operationalize-production/Makefile) | Install, monitor, policy, validate, and clean targets. |
| [`src/__init__.py`](../scripts/operationalize-production/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/monitor.py`](../scripts/operationalize-production/src/monitor.py) | `check_ner_service_health()` probes `/ner`; `evaluate_alerts()` is the pure alert decision; `main()` reads `audit_log_governance_stats()` and `fetch_hallucinated_audit_samples()`. |
| [`src/policy_registry.py`](../scripts/operationalize-production/src/policy_registry.py) | `load_history()`, `latest_entry()`, and `check_drift()` against `HISTORY_PATH`. |
| [`src/policy_history.json`](../scripts/operationalize-production/src/policy_history.json) | Checked-in record of approved tiering-policy versions. |
| [`src/validate.py`](../scripts/operationalize-production/src/validate.py) | Alert-logic and drift tests (no live services), plus live `ensure_audit_retention_policy()` and `audit_log_governance_stats()` checks. |
| [`src/hardening/bedrock_invoke_policy.json`](../scripts/operationalize-production/src/hardening/bedrock_invoke_policy.json) | Illustrative IAM policy scoped to specific Bedrock model ARNs. |
| [`src/hardening/opensearch_roles.json`](../scripts/operationalize-production/src/hardening/opensearch_roles.json) | Illustrative `recipe_writer`, `recipe_reader`, `audit_writer`, `audit_admin` roles. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `ensure_audit_retention_policy()`, `get_audit_retention_policy_status()`, `audit_log_governance_stats()`, `fetch_hallucinated_audit_samples()`, `AUDIT_ISM_POLICY_ID`. |
| [Operational checklist](OPERATIONAL_CHECKLIST.md) | Automated versus manual hardening items. |
| [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md#failure-and-monitoring) | Why the NER health check exists. |
| [Troubleshooting](../troubleshooting/operationalize-production.md) | Setup and runtime failures for this task. |
