# Task 1: Prepare the workspace

Every task in this lab runs against the same Instaclustr OpenSearch cluster and the same LLM provider, from one Python environment and one `.env` file. In this task you prepare that shared workspace once, then run a setup check before you start Baseline Vector RAG.

## What you will learn

You will clone the lab, create its Python environment, install the shared dependencies, configure the services every task uses, and run a setup check that reports problems by name without printing a secret.

The practical sequence is:

| Step | Activity | Checkpoint |
| --- | --- | --- |
| [1](#step-1-enter-the-existing-workspace) | Clone the lab and open its root. | `README.md`, `architecture.md`, `scripts/`, and `support/` are present. |
| [2](#step-2-set-up-the-environment) | Create `.venv` and install dependencies. | `pip` and the spaCy model download finish without errors. |
| [3](#step-3-configure) | Create `.env` from `support/.env.example` and fill it in. | `.env` exists at the lab root with real values. |
| [4](#step-4-verify-the-workspace) | Run `support/validate_setup.py`. | It prints `Setup check passed.` |
| [5](#step-5-pick-a-task) | Choose where to start. | You know which task page to open next. |

## Understand the workspace

The [environment reference](../references/setup-environment.md) records the exact settings, files, and writes involved. How the code is organized, and what is shared between tasks versus specific to one, is described in [`architecture.md`](../architecture.md#repository-layout). The sections below explain what you need and why.

### Accounts you'll need

- An [Instaclustr](https://www.instaclustr.com/) OpenSearch cluster. Every task shares one cluster; each task just uses different index names.
- An LLM provider. Pick one:
  - An AWS account with **Bedrock model access enabled** for at least one Anthropic Claude model (AWS console → Bedrock → Model access; this is a separate opt-in step per model, not automatic), **or**
  - An OpenAI (or OpenAI-compatible) API key, with no AWS account needed. Set `LLM_PROVIDER=openai` in `.env` (see step 3).
- (Optional) A [HuggingFace](https://huggingface.co/) token. The first run downloads the embedding model from HuggingFace; setting `HF_TOKEN` in `.env` gives higher download rate limits and removes the "unauthenticated requests" warning. Nothing requires it, and the recipe sample is already included, so no dataset download is needed.

The cluster and the LLM provider are paid services, so check each provider's pricing or trial terms first. The query steps in Tasks 2 to 6 make LLM calls that your provider bills.

## Before you begin

You need a Bash or zsh terminal, Git, and Python 3.10 or newer with `venv` (Python 3.12 is the default recommended version). The accounts above must exist, and for Bedrock, model access must already show as granted in the AWS console.

Installing dependencies downloads packages from PyPI and the spaCy model. Running any task later also downloads the embedding model into `.cache/` on first use. Keep `.env` private: it holds your cluster password and LLM credentials, and it is git-ignored for that reason.

## Step 1: enter the existing workspace

Clone the [instaclustr/labs](https://github.com/instaclustr/labs) repository and open a terminal in this lab's root directory, the one containing `README.md`:

```bash
git clone https://github.com/instaclustr/labs.git
cd labs/rag-retrieval
ls README.md architecture.md scripts support
```

**Expected:** the listing shows `README.md`, `architecture.md`, and the contents of `scripts/` (six task directories) and `support/`. A missing entry means you're in the wrong directory or have an incomplete copy.

## Step 2: set up the environment

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r support/requirements.txt
python -m spacy download en_core_web_sm   # needed by Baseline BM25 RAG's ner_service.py, which Hybrid RAG (NER + Rerank) through Operationalize Production reuse
```

Use `python3.12` above for the default environment, or the executable for another installed Python 3.10+ version. Dependency availability still depends on that interpreter and platform.

**Expected:** each command finishes without an error, and your prompt shows the activated `.venv`. The shared requirements install spaCy 3.8.16 and the official OpenAI Python SDK. The spaCy download selects a compatible `en_core_web_sm` model. Leave this shell active; every later task reuses it. If the spaCy download fails, see [setup troubleshooting](../troubleshooting/setup-environment.md).

## Step 3: configure

```bash
cp support/.env.example .env
# then edit .env: OPENSEARCH_HOST/USER/PASSWORD, and either --
#   Bedrock (default): AWS_ACCESS_KEY_ID/SECRET_ACCESS_KEY, AWS_REGION, and
#   BEDROCK_MODEL_ID (exact ID from Bedrock's Model access page), or
#   OpenAI: set LLM_PROVIDER=openai, then OPENAI_API_KEY and OPENAI_MODEL.
```

**Expected:** `.env` exists at the lab root, next to `README.md`. Every task finds it there through [`load_settings()`](../support/common/config.py), whichever `scripts/<task-id>/` directory you run from.

**If someone else uses the same cluster,** index names are global to the cluster, so two people running the lab would overwrite each other's data. Give your indexes a suffix by adding these lines to `.env` (replace `yourname`):

```bash
OPENSEARCH_VECTOR_RECIPES_BASELINE_INDEX=recipes-vector-baseline-yourname
OPENSEARCH_BM25_RECIPES_BASELINE_INDEX=recipes-bm25-baseline-yourname
OPENSEARCH_HYBRID_INDEX=recipes-hybrid-yourname
OPENSEARCH_RECIPE_BM25_INDEX=recipes-bm25-yourname
OPENSEARCH_RECIPE_VECTOR_INDEX=recipes-vector-yourname
OPENSEARCH_AUDIT_INDEX=recipes-audit-log-yourname
```

**Expected:** Step 4 lists these names in its `index-names` line. Later task pages show the default index names in their `Expected:` lines (for example `'recipes-bm25-baseline'`); with a suffix, expect your suffixed names instead. Two objects are not covered by the suffix, because they are created once per cluster and shared by everyone: the hybrid search pipeline `hybrid-search-pipeline` (Hybrid RAG (Native)) and the audit-log retention policy `recipes-audit-log-retention` (Operationalize Production). The pipeline is rewritten every time Hybrid RAG (Native)'s `ingest.py` or `query.py` runs (`ingest.py` with the weights you pass, `query.py` always with the defaults, 0.3 BM25 and 0.7 vector), so running that task changes it for everyone on the cluster. The policy is created only if it doesn't already exist and is then attached to your audit index, so if it already exists you share the retention window of whoever created it.

## Step 4: verify the workspace

[`support/validate_setup.py`](../support/validate_setup.py) checks the workspace without connecting to anything. It checks the Python version, the folder layout, the installed packages, the spaCy model, the `.env` file, and the required settings (using the same [`require_configured()`](../support/common/config.py) check every task runs). It reports missing settings by name, never their values.

**Mini-task; activated `.venv`, from the lab root:**

```bash
python support/validate_setup.py
```

**Expected:** a `[PASS]` line for each of `python`, `layout`, `dependencies`, `spacy-model`, `env-file`, `configuration`, and `index-names`, plus `aws-credentials` if `LLM_PROVIDER` is `bedrock` (the default), followed by `Setup check passed.` and exit code 0. Sample output (the `python` line shows your version, `index-names` lists the names from your `.env`, and `aws-credentials` names the credential source it found):

```text
[PASS] python: Python 3.12 (recommended version)
[PASS] layout: README, architecture, support and 6 task folders present
[PASS] dependencies: 12 required packages importable
[PASS] spacy-model: spaCy 3.8.16; model en_core_web_sm loaded
[PASS] env-file: .env found at the lab root
[PASS] configuration: required OpenSearch and LLM settings are present
[PASS] index-names: index names in use: OPENSEARCH_VECTOR_RECIPES_BASELINE_INDEX=recipes-vector-baseline, ...
[PASS] aws-credentials: AWS credentials found (source: env); Bedrock model access is not checked here
Setup check passed.
```

If a line says `[FAIL]`, it names what is missing (for example `OPENSEARCH_HOST is not set to a real Instaclustr host`). Fix it in `.env` or the environment and rerun. `[SKIP]` lines mean an earlier failure blocked that check, and a `[WARN]` line does not fail the run. The version check accepts Python 3.10+ without warning merely because it is newer than 3.12. The `aws-credentials` check only looks for credentials; it doesn't call AWS, so it can't tell whether Bedrock model access is granted.

The offline check confirms that values are present, not that the cluster or model accepts them. To also confirm that the cluster answers and the embedding model loads, add the two optional flags (a third, `--check-ner`, confirms the local NER service answers once you've started it in Baseline BM25 RAG):

```bash
python support/validate_setup.py --check-opensearch --check-embeddings
```

**Expected:** two more `[PASS]` lines, `opensearch: cluster reachable, OpenSearch <version>; neural-search plugin: yes; index-management (ISM) plugin: yes` and `embeddings: sentence-transformers/all-MiniLM-L6-v2 loaded, 384 dimensions`. The first run downloads the embedding model, so it takes longer. `opensearch` is read-only: it asks the cluster for its version and plugin list and changes nothing. If it prints `[WARN]` with `neural-search plugin: NO`, Hybrid RAG (Native) will not work on that cluster. A missing ISM plugin only means Operationalize Production reports `[SKIP]` for retention. If the check fails, check the host, port, credentials, and your cluster's IP allow-list.

## Step 5: pick a task

Pick a task and follow its task page, starting with [Baseline Vector RAG](baseline-vector-rag.md) if you want the full progression, or jump straight to whichever failure mode you're curious about. Every task page has its own numbered steps and a completion checkpoint with sample output.

## Completion checkpoint

The workspace is ready when `.venv` is active with `support/requirements.txt` and `en_core_web_sm` installed, `.env` sits at the lab root with real values, and `python support/validate_setup.py` prints `Setup check passed.`

The [environment source map](../references/setup-environment.md#source-map) lists the files behind these checks. Resolve failures through [setup troubleshooting](../troubleshooting/setup-environment.md) before advancing. To see what each service you just configured is for, and what runs where, read [Infrastructure choices](../concepts/INFRASTRUCTURE_CHOICES.md).

Continue to [Task 2: Baseline Vector RAG](baseline-vector-rag.md), or return to the [task index](../README.md#tasks).
