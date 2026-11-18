[Task 4: Hybrid RAG (Native)](../tasks/hybrid-rag-native.md)

# Troubleshooting

Common setup and runtime issues for this task, and how to fix them. If
something isn't covered here, check the [task page](../tasks/hybrid-rag-native.md) first
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
access page (copy it exactly, including any prefix), and it must be available in
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

### `hybrid` query or search pipeline rejected by the cluster

`make ingest` and `make query` both create the `hybrid-search-pipeline` search pipeline, and the query then uses OpenSearch's `hybrid` query type. Both come from the neural-search plugin, which the Instaclustr console calls AI Search. On a cluster without it, one of those calls fails with an OpenSearch error that names the `hybrid` query or the `normalization-processor` as unknown, and nothing in the lab catches it, so you see a traceback. The failure comes from the cluster, not from your `.env` or your data.

Confirm it from the lab root with the `.venv` active:

```bash
python support/validate_setup.py --check-opensearch
```

**Expected:** a `[WARN]` line saying `neural-search plugin: NO` means this cluster cannot run Hybrid RAG (Native). Use a cluster that has the plugin (see [Your OpenSearch cluster](../tasks/setup-environment.md#your-opensearch-cluster)), or ask its administrator to enable it. The other tasks do not use the `hybrid` query, so they still work.

## Runtime

### `OldLayoutIndexError: Index ... has the older one-document-per-recipe layout`

The index named in the message already exists, and its mapping has no `chunk_id` field, so it was built by an earlier version of this lab that stored one document per recipe. A query never deletes an index, so it stops here. Run this task's ingest (`make ingest`, or `python -m src.ingest`), which replaces that index with the chunk layout and logs a `WARNING` when it does. If the index belongs to someone else, do not run ingest: give your own indexes a suffix first (Task 1, Step 3).

### OpenSearch connection suddenly fails after previously working

If `OPENSEARCH_HOST` hasn't changed but connections still fail, verify the
value in your *current* `.env` against the Instaclustr console — a
cluster's load-balancer hostname can change (e.g. after a resize/restore),
and a stale hostname produces a generic connection-timeout error that
looks like a network problem, not a config problem.

A second common cause is the cluster's firewall rules (its IP allow-list). If
your public IP changed (a different network, a VPN, or a corporate proxy that
rotates its address), the cluster stops answering and connections hang until
they time out. The OpenSearch client waits up to 60 seconds and retries three
times, so a command can look frozen for several minutes. To test without
waiting:

```bash
curl -s -o /dev/null -m 15 -w "%{http_code}\n" https://<your OPENSEARCH_HOST>:9200/
```

`401` means the cluster is reachable (it wants a password). `000` after the
15 seconds means it is not answering from this machine: add your current public
IP to the cluster's firewall rules in the Instaclustr console.

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

Rerun the step that failed, then return to the [completion checkpoint](../tasks/hybrid-rag-native.md#completion-checkpoint) once its checks pass. The [source map](../references/hybrid-rag-native.md#source-map) lists the files behind each step.
