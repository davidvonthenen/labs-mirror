[Task 3: Baseline BM25 RAG](../tasks/baseline-bm25-rag.md)

# Troubleshooting

Common setup and runtime issues for this task, and how to fix them. If
something isn't covered here, check the [task page](../tasks/baseline-bm25-rag.md) first
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

`ner_service.py` (Baseline BM25 RAG, Hybrid RAG (NER + Rerank) and Governance Tiering) needs spaCy's small English model,
which isn't installed by `pip install -r support/requirements.txt` alone:

```bash
python -m spacy download en_core_web_sm
```

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

### `AccessDeniedException` calling Bedrock

Bedrock model access is an explicit **per-model opt-in** in the AWS
console (Bedrock → Model access), separate from having Bedrock IAM
permissions at all. Check that the exact model in `BEDROCK_MODEL_ID` shows
as "Access granted" there — not just that your IAM user/role has
`bedrock:InvokeModel`.

### `ValidationException: The provided model identifier is invalid`

`BEDROCK_MODEL_ID` must be the exact model ID string from Bedrock's Model
access page (e.g.
`anthropic.claude-3-5-sonnet-20241022-v2:0`), and it must be available in
the region set by `AWS_REGION` — Bedrock model availability varies by
region.

### `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported`

Some newer models can't be called with their plain model ID. Bedrock asks for an
**inference profile** instead: an ID that usually starts with a region code (for
example `us.` in front of the provider name), listed in the Bedrock console under
Cross-region inference. Put that profile ID in `BEDROCK_MODEL_ID` and keep
`AWS_REGION` in a region the profile covers. Your IAM permissions then also have
to allow `bedrock:InvokeModel` on the profile. Which models need this changes
over time, so treat the console as the source of truth.

## Runtime

### Query results look wrong / entity matching isn't kicking in

Check whether `ner_service.py` is actually running (Baseline BM25 RAG, Hybrid RAG (NER + Rerank) and Governance Tiering
all depend on it). `support/common/ner_client.py`'s `NERClient` deliberately
**swallows** connection failures into an empty entity list rather than
raising, so a downed NER service degrades silently to plain full-text
matching instead of erroring — you'll get an answer, just a worse one,
with no error message telling you why. Operationalize Production's `monitor.py` exists
specifically to catch this in production; in dev, just check the
terminal you started it in.

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

Rerun the step that failed, then return to the [completion checkpoint](../tasks/baseline-bm25-rag.md#completion-checkpoint) once its checks pass. The [source map](../references/baseline-bm25-rag.md#source-map) lists the files behind each step.
