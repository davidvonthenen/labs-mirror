[Task 1: Prepare the workspace](../tasks/setup-environment.md)

# Troubleshooting

Use after a failure in [workspace setup](../tasks/setup-environment.md). These
entries also appear in each task's own troubleshooting page.

## Setup

### `[FAIL]` lines from `validate_setup.py`

`python support/validate_setup.py` prints one `[PASS]` or `[FAIL]` line per check and names what is wrong, never a value. Run it from the lab root with the `.venv` active.

| Failing check | Meaning and fix |
| --- | --- |
| `python` | Python is older than 3.10. Recreate `.venv` with Python 3.12, the default recommended version, or another Python 3.10+ interpreter supported by the dependencies. |
| `layout` | A file or task folder is missing (including `support/recipes/recipes_sample.json`), or you're running a copy that isn't the lab root. Open the directory containing `README.md` and `scripts/`. |
| `dependencies` | A package is missing, usually because `.venv` isn't active. Activate it and rerun `pip install -r support/requirements.txt`. |
| `spacy-model` | See the `en_core_web_sm` entry below. |
| `env-file` | No `.env` in the current directory or the lab root. Run `cp support/.env.example .env` from the lab root and fill it in. |
| `configuration` | See the `Missing configuration` entry below; the line lists the setting names. |
| `aws-credentials` | No credentials were found for Bedrock. Set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` in `.env` (empty values count as unset), configure an AWS profile or SSO login, or set `LLM_PROVIDER=openai`. This only looks for credentials; Bedrock model access is checked when a task first calls the model. |
| `opensearch` (with `--check-opensearch`) | The cluster didn't answer. Check `OPENSEARCH_HOST`, `OPENSEARCH_PORT`, the credentials, and your cluster's IP allow-list. The report shows only the error class, not the host. A `[WARN]` saying `neural-search plugin: NO` means Hybrid RAG (Native) can't run on this cluster; see [that task's troubleshooting](hybrid-rag-native.md#hybrid-query-or-search-pipeline-rejected-by-the-cluster). |
| `ner` (with `--check-ner`) | The local NER service didn't answer. Start it with `python -m src.ner_service` in `scripts/baseline-bm25-rag` (see Baseline BM25 RAG, step 2) and check `NER_SERVICE_URL`. |
| `embeddings` (with `--check-embeddings`) | The model failed to download or load. Check network access to HuggingFace; a corporate proxy may need the certificate steps below. |

A `[SKIP]` line means an earlier check failed and blocked this one. A `[WARN]` line doesn't fail the run.

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

After upgrading spaCy, reinstall a matching model rather than keeping an older English-model package. The setup validator checks both compatibility and loading. For an existing environment, run:

```bash
python -m pip install --upgrade pip
python -m pip install --upgrade -r support/requirements.txt
python -m spacy download en_core_web_sm
python -m spacy validate
```

If the environment still contains dependency conflicts from an older installation, recreate `.venv` with Python 3.12 and repeat Task 1. Do not restore the old setuptools, click, or NumPy caps. The workshop does not use negspacy.

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

## Return to the task

Rerun the failed check, then return to the [setup completion checkpoint](../tasks/setup-environment.md#completion-checkpoint). The [environment source map](../references/setup-environment.md#source-map) lists the files involved.
