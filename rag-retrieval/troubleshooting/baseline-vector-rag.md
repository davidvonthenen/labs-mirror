[Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md)

# Troubleshooting

Common setup and runtime issues for this task, and how to fix them. If
something isn't covered here, check the [task page](../tasks/baseline-vector-rag.md) first
— several describe the expected output for each step, which can help
distinguish a real bug from expected behavior (e.g. Governance Tiering's escalation).

## Setup

### `RuntimeError: Missing configuration in the environment or optional .env`

`support/common/config.py`'s `require_configured()` checks cluster settings
and, for generation, the selected provider's settings. The message names
missing values: `OPENSEARCH_HOST`, `OPENSEARCH_USER`, `OPENSEARCH_PASSWORD`,
then `OPENAI_API_KEY` / `OPENAI_MODEL` for the default `openai` provider,
`ANTHROPIC_API_KEY` / `ANTHROPIC_MODEL` for `anthropic`, or
`BEDROCK_MODEL_ID` for `bedrock`. Bedrock also needs valid AWS credentials.
Ingestion does not require generation credentials. For provider authentication, model,
or parameter failures, see [provider request errors](setup-environment.md#provider-authentication-model-or-parameter-errors).

Configure the named values through `insta config`, the inherited environment,
or a lab-root `.env` copied from `support/.env.example`. Do not overwrite an
existing configured file or print secret values while diagnosing. An old
placeholder host (`your-cluster-host.instaclustr.com`) also fails this check.

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

### Bedrock authorization errors

The `anthropic.AnthropicBedrock` client authenticates with AWS credentials.
Verify the active AWS identity/profile, the configured region, access to the
selected Claude model or inference profile, and permission to invoke it.
An Anthropic API key does not grant AWS access. The setup credential check
only discovers credentials; it does not verify model authorization.

### `ValidationException: The provided model identifier is invalid`

`BEDROCK_MODEL_ID` must be an exact Claude model ID or supported inference
profile ID for your AWS account and `AWS_REGION`. A direct Anthropic API model
ID is not interchangeable with a Bedrock identifier. Check the selected
provider's current model listing and access configuration.

### `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported`

Some newer models can't be called with their plain model ID. Bedrock asks for an
**inference profile** instead: an ID that usually starts with a region code (for
example `us.` in front of the provider name), listed in the Bedrock console under
Cross-region inference. Put that profile ID in `BEDROCK_MODEL_ID` and keep
`AWS_REGION` in a region the profile covers. Your IAM permissions then also have
to allow `bedrock:InvokeModel` on the profile. Which models need this changes
over time, so treat the console as the source of truth.

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

Rerun the step that failed, then return to the [completion checkpoint](../tasks/baseline-vector-rag.md#completion-checkpoint) once its checks pass. The [source map](../references/baseline-vector-rag.md#source-map) lists the files behind each step.
