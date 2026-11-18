[Task 1: Prepare the workspace](../tasks/setup-environment.md)

# Troubleshooting

Use after a failure in [workspace setup](../tasks/setup-environment.md). Some of
these entries are repeated on each task's own troubleshooting page. The `[FAIL]` table,
the `scipy` import error, and the two `huggingface.co` entries (`403` and `404 Not Found` lines) exist only here, so come back
to this page for them from any task.

## Setup

### `[FAIL]` lines from `validate_setup.py`

`python support/validate_setup.py` prints one `[PASS]` or `[FAIL]` line per check and names what is wrong, never a value. Run it from the lab root with the `.venv` active.

| Failing check | Meaning and fix |
| --- | --- |
| `python` | Python is older than 3.10. Recreate `.venv` with Python 3.12, the default recommended version, or another Python 3.10+ interpreter supported by the dependencies. |
| `layout` | A file or task folder is missing (including `support/recipes/recipes_sample.json` and `support/recipes/recipes_synthetic.json`), or you're running a copy that isn't the lab root. Open the directory containing `README.md` and `scripts/`. |
| `dependencies` | Either `not installed: ...` (usually because `.venv` isn't active; activate it and rerun `pip install -r support/requirements.txt`), or `installed but cannot be imported: <package> (ImportError)`. The second is almost always the `scipy` problem described in the entry below. |
| `spacy-model` | See the `en_core_web_sm` entry below. |
| `env-file` | No `.env` in the current directory or the lab root. Run `cp support/.env.example .env` from the lab root and fill it in. |
| `configuration` | See the `Missing configuration` entry below; the line lists the setting names. |
| `aws-credentials` | No credentials were found for Bedrock. Set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` in `.env` (empty values count as unset), configure an AWS profile or SSO login, or set `LLM_PROVIDER=openai`. This only looks for credentials; Bedrock model access is checked when a task first calls the model. |
| `opensearch` (with `--check-opensearch`) | The cluster didn't answer. Check `OPENSEARCH_HOST`, `OPENSEARCH_PORT`, the credentials, and your cluster's IP allow-list (an IP that changed because of a new network, VPN or proxy is a common cause). A blocked connection hangs rather than failing at once: the client waits up to 60 seconds and retries three times, so this check can take several minutes. To test in 15 seconds, run `curl -s -o /dev/null -m 15 -w "%{http_code}\n" https://<your OPENSEARCH_HOST>:9200/`: `401` means the cluster is reachable, `000` means it is not answering from this machine. The report shows only the error class, not the host. A `[WARN]` saying `neural-search plugin: NO` (the AI Search plugin in the Instaclustr console) means Hybrid RAG (Native) can't run on this cluster; see [that task's troubleshooting](hybrid-rag-native.md#hybrid-query-or-search-pipeline-rejected-by-the-cluster). |
| `ner` (with `--check-ner`) | The local NER service didn't answer. Start it with `python -m src.ner_service` in `scripts/baseline-bm25-rag` (see Baseline BM25 RAG, step 2) and check `NER_SERVICE_URL`. |
| `embeddings` (with `--check-embeddings`) | The model failed to download or load. Check network access to HuggingFace; a corporate proxy may need the certificate steps below, or may block `huggingface.co` outright (see the `403` entry below). |

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

### `ImportError: dlopen(... scipy/sparse/linalg/_propack/_spropack ...)`

Every import of `sentence-transformers` fails, so every ingest and query fails, and `validate_setup.py` reports
`[FAIL] dependencies: installed but cannot be imported: sentence-transformers (ImportError)`. `scipy` is not in the lab's
own list; it is installed because `sentence-transformers` needs it. On Python 3.10, the 1.15.x releases of `scipy` contain a compiled
library that macOS 27 refuses to load (`section '__DATA/__thread_bss' has a zero-fill section type, but offset field is not zero`).
`requirements.txt` pins `scipy<1.15` on Python 3.10 for that reason; Python 3.11 and newer get a current `scipy`, which loaded
correctly when tested on 3.11. If you installed before that pin existed, or installed the packages by hand:

```bash
pip install -r support/requirements.txt   # or: pip install "scipy<1.15"
python support/validate_setup.py
```

### `403` from `huggingface.co`, or `We couldn't connect to 'https://huggingface.co'`

The first run downloads the embedding model (about 90 MB) into the lab's `.cache/huggingface/`. If `curl -sI https://huggingface.co`
shows `server: Zscaler` or another proxy's name, the proxy is blocking the site by policy, and no setting in the lab changes that.
The certificate steps above do not apply to this case, because the request is refused, not re-signed. Two options:

1. Ask your IT team to allow `huggingface.co` (and the download host it redirects to).
2. Download the model on a machine that can reach it (running `python support/validate_setup.py --check-embeddings` there is enough),
   then copy that machine's `.cache/huggingface/` folder into this lab's `.cache/` folder. The lab finds the model there without a
   download.

### `404 Not Found` lines from `huggingface.co` in the log

You may see lines like these when a pipeline loads the embedding model:

```text
HTTP Request: GET https://huggingface.co/api/models/sentence-transformers/all-MiniLM-L6-v2/tree/main/additional_chat_templates ... "HTTP/1.1 404 Not Found"
HTTP Request: HEAD https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/<revision>/2_Normalize/config.json "HTTP/1.1 404 Not Found"
```

This is expected and harmless. The libraries ask for optional files (a chat-templates folder and a normalization config) that this model does
not have, and carry on without them. A Hugging Face token does not change this. This model is public, so it downloads without one, and an
authentication problem would show up as `401` or `403`, not `404`. The `HF_TOKEN` in `.env` only affects download rate limits and the
"unauthenticated requests" warning.

The lab hides these HTTP lines by default. If you still see them, you are probably running with `LOG_LEVEL=DEBUG`, or you are using
a copy of the lab from before `httpx2` was added to the quiet loggers in `support/common/logging.py`.

To stop the library contacting Hugging Face at all after the model has been downloaded once (the lab keeps it in `.cache/huggingface/`),
set this in the terminal before you run a task:

```bash
export HF_HUB_OFFLINE=1
```

Unset it (`unset HF_HUB_OFFLINE`) if you later need to download a different model.

## Return to the task

Rerun the failed check, then return to the [setup completion checkpoint](../tasks/setup-environment.md#completion-checkpoint). The [environment source map](../references/setup-environment.md#source-map) lists the files involved.
