[Task 7: Operationalize Production](../tasks/operationalize-production.md)

# Troubleshooting

Common setup and runtime issues for this task, and how to fix them. If
something isn't covered here, check the [task page](../tasks/operationalize-production.md) first
— several describe the expected output for each step, which can help
distinguish a real bug from expected behavior (e.g. Governance Tiering's escalation).

## Setup

### `RuntimeError: Missing configuration in .env`

Raised by `support/common/config.py`'s `require_configured()`, which every task
calls before connecting to anything. The message lists each problem it found:
an empty or placeholder `OPENSEARCH_HOST`, an empty `OPENSEARCH_USER` or
`OPENSEARCH_PASSWORD`, and an empty `BEDROCK_MODEL_ID` (or, with
`LLM_PROVIDER=openai`, an empty `OPENAI_API_KEY` or `OPENAI_MODEL`). Fix: `cp
support/.env.example .env` if you haven't already, then fill in real values. A
stale copy of `.env.example` with the placeholder host
(`your-cluster-host.instaclustr.com`) triggers this same error.

The check does **not** recognise the placeholder user and password from
`.env.example` (`your-username`, `your-password`). If you set the host but leave
those, the check passes and OpenSearch then rejects the connection with an
authentication error (HTTP 401). Replace them with your real credentials.

### `OSError: [E050] Can't find model 'en_core_web_sm'`

Step 2 starts `ner_service.py` (from `scripts/baseline-bm25-rag/`), which needs spaCy's small English model. `pip install -r support/requirements.txt` does not install it:

```bash
python -m spacy download en_core_web_sm
```

If the model download or load fails, follow the [setup troubleshooting entry](setup-environment.md#oserror-e050-cant-find-model-en_core_web_sm) to install a model compatible with the current spaCy version and check the environment's dependencies.

### `CERTIFICATE_VERIFY_FAILED: unable to get local issuer certificate`

Two common causes, both handled automatically by `support/common/__init__.py` on
import — if you still see this, something's preventing that self-heal:

1. **Python installed via the official python.org `.pkg` installer**
   doesn't wire itself up to any certificate trust store, so *any* HTTPS
   call fails, network restrictions aside.
2. **A corporate TLS-inspecting proxy** (Zscaler, Netskope, etc.) re-signs
   HTTPS traffic with a certificate the OS keychain trusts but a plain
   `certifi` bundle does not.

`support/common/__init__.py` merges macOS System-keychain roots into a local
combined bundle (`.cache/merged-ca-bundle.pem`) and points
`SSL_CERT_FILE`/`REQUESTS_CA_BUNDLE`/`AWS_CA_BUNDLE` at it automatically.
This is macOS-specific (`sys.platform == "darwin"`) and best-effort — on
Linux/Windows, or if this still doesn't resolve it, fall back to
installing your proxy's root CA into `certifi`'s bundle manually, or set
`SSL_CERT_FILE`/`REQUESTS_CA_BUNDLE` yourself before running any task.

## Runtime

### `NER service is not responding` alert from `monitor.py`

`monitor.py` posts a short test sentence to the NER service (`NER_SERVICE_URL`, default `http://127.0.0.1:8000/ner`) and raises this alert if it does not answer with HTTP 200 and JSON. Check that `ner_service.py` is still running in the terminal from Step 2; if not, start it again with `python -m src.ner_service` in `scripts/baseline-bm25-rag/` and rerun `monitor`.

The alert exists because a downed service raises no error anywhere else. `support/common/ner_client.py`'s `NERClient` turns connection failures into an empty entity list, so Baseline BM25 RAG, Hybrid RAG (NER + Rerank) and Governance Tiering quietly lose the entity boost and return plainer matches. Hard `--exclude` and `--require` filters still apply.

### `Address already in use` starting `ner_service.py`

Default port is 8000 (`PORT` env var to override). If Baseline BM25 RAG's
`ner_service.py` is already running, Hybrid RAG (NER + Rerank) and Governance Tiering's `query.py` can reuse it
directly — no need to start a second copy (each task page that needs the
service says to reuse a running one).

### OpenSearch connection suddenly fails after previously working

If `OPENSEARCH_HOST` hasn't changed but connections still fail, verify the
value in your *current* `.env` against the Instaclustr console — a
cluster's load-balancer hostname can change (e.g. after a resize/restore),
and a stale hostname produces a generic connection-timeout error that
looks like a network problem, not a config problem.

### `TransportError` / warnings from Operationalize Production's `validate.py` mentioning `_plugins/_ism`

Some managed OpenSearch tiers restrict or disable the ISM (Index State
Management) plugin. `ensure_audit_retention_policy()` is written to
degrade gracefully — it returns `False` and logs a warning rather than
raising, and Operationalize Production's `validate.py` reports `[SKIP]` rather than failing
in this case. Treat it as a manual-review item (see
[`OPERATIONAL_CHECKLIST.md`](../references/OPERATIONAL_CHECKLIST.md) section A), not a bug.

### Governance Tiering and Operationalize Production's `recipes-audit-log` queries return nothing

`monitor.py` (through `audit_log_governance_stats()`) reads only the audit records
inside a trailing `timestamp` window (default 24 hours, set by
`GOVERNANCE_ALERT_LOOKBACK_HOURS`). Governance Tiering still writes a record for
every query; the window only limits what the monitor reads. If the records you
expect are older, pass a larger `--lookback-hours` to `monitor.py`, or query the
index directly without a time filter. If the index is empty, run a Governance
Tiering query first.

### Results differ from a task's documented sample output

Every task page's **Expected** notes describe the properties to check, and
some include sample output from a real run (exact scores, exact escalation
reasons). Small score differences are expected — they depend on the exact state of your
index (re-running `ingest.py` doesn't guarantee identical document scoring
if you've ingested extra/fewer documents, or if your OpenSearch cluster
version differs). The *qualitative* result (which recipe wins, whether
escalation triggers) should still match; if it doesn't, check that
`ingest.py` completed successfully and the expected recipe count landed in
the index before assuming a real bug.

## Return to the task

Rerun the step that failed, then return to the [completion checkpoint](../tasks/operationalize-production.md#completion-checkpoint) once its checks pass. The [source map](../references/operationalize-production.md#source-map) lists the files behind each step.
