[Task 1: Prepare the workspace](../tasks/setup-environment.md)

# Workspace environment reference and source map

## Workspace settings

| Property | Behavior |
| --- | --- |
| Python | A local virtual environment at the lab root (`.venv`). Python 3.12 is the default recommended version, and 3.10 and newer are accepted. Dependencies must also support the chosen interpreter and platform; spaCy 3.8.16 currently declares Python >=3.9,<3.15, so the current dependency set does not promise Python 3.15+ installation. |
| Dependencies | One shared set in `support/requirements.txt`, plus spaCy's `en_core_web_sm` model for the NER service. spaCy is pinned to 3.8.16, the latest stable release verified on October 14, 2026. The official OpenAI and Anthropic Python SDKs are included; the Anthropic dependency includes its Bedrock integration. The obsolete setuptools and click caps are removed, and NumPy 2 is allowed. Download the English model again after upgrading an existing environment. On Python 3.10, `scipy` is held below 1.15 so that `sentence-transformers` stays importable on macOS 27, where the compiled library in the 1.15.x wheels is refused by the system loader (`scipy` is installed as a dependency of `sentence-transformers`, so it appears only as a pin). |
| Configuration | Inherited process environment plus an optional lab-root `.env`, copied from `support/.env.example`. `load_settings()` reads the first file found: `./.env` relative to the current directory, then the lab-root `.env`. Existing environment variables take precedence. A file is not required by `insta` or direct coding-tool runs. |
| OpenSearch | One Instaclustr cluster shared by every task; each task uses its own index names. On a cluster shared with other people, override the six `OPENSEARCH_*_INDEX` names in `.env` with a suffix (see Task 1, step 3). The `hybrid-search-pipeline` search pipeline and the `recipes-audit-log-retention` ISM policy have fixed names and are shared per cluster. |
| LLM | `LLM_PROVIDER=openai` by default, using `openai.OpenAI`; `anthropic` uses `anthropic.Anthropic`; `bedrock` uses `anthropic.AnthropicBedrock` for Claude models with AWS authentication. |
| Local services | The NER service from Baseline BM25 RAG on `127.0.0.1:8000`; no other local service. |
| Writes | `.cache/` at the lab root (merged CA bundle and HuggingFace model cache), OpenSearch indexes on your cluster, and `__pycache__` folders. `.env` and `.cache/` are git-ignored. |
| Working directory | Every task runs from its own `scripts/<task-id>/` directory, with `python -m src.<module>` or `make <target>`. |

Keep `.env` out of anything you share: it holds your OpenSearch password and LLM credentials.

| Provider setting | Credentials | Model and endpoint settings |
| --- | --- | --- |
| `LLM_PROVIDER=openai` (default) | `OPENAI_API_KEY` | `OPENAI_MODEL`; `OPENAI_BASE_URL` defaults to `https://api.openai.com/v1` and can select a compatible endpoint. |
| `LLM_PROVIDER=anthropic` | `ANTHROPIC_API_KEY` | `ANTHROPIC_MODEL`, using the Anthropic API. |
| `LLM_PROVIDER=bedrock` | AWS credential resolution; `AWS_PROFILE` or environment credentials, with `AWS_SESSION_TOKEN` for temporary credentials | `BEDROCK_MODEL_ID` (Claude model or inference profile ID) and `AWS_REGION`. No Anthropic API key is required. |

Use a model identifier authorized for the selected account and region. Setup
checks the presence of settings and, for Bedrock, credential discovery; it does
not establish that a model invocation will succeed. Current validation evidence
is in [VALIDATION_REPORT.md](../VALIDATION_REPORT.md).

`BEDROCK_MAX_TOKENS` retains its existing name and defaults to `1024` for all
providers. `BEDROCK_TEMPERATURE` is optional: unset or blank omits temperature;
an explicit numeric value requires a model that accepts it. These settings do
not change chunking or embedding generation.

The prebuilt container image has its own source snapshot. Its provider support
does not change when this checkout or Compose environment is updated. Use the
updated local source for this revision, or an image deliberately rebuilt from
the updated source; a provider setting alone does not update image contents.

## Source map

| Source | Responsibility |
| --- | --- |
| [`support/.env.example`](../support/.env.example) | Every setting for every task, with placeholders. |
| [`support/requirements.txt`](../support/requirements.txt) | The single shared dependency set. |
| [`support/common/config.py`](../support/common/config.py) | `Settings`, `load_settings()`, and `require_configured()`, which lists missing values. |
| [`support/common/__init__.py`](../support/common/__init__.py) | Merged CA bundle and project-local HuggingFace cache under `.cache/`. |
| [`support/validate_setup.py`](../support/validate_setup.py) | The setup check from Task 1: Python version (recommends 3.12), layout, packages, spaCy model compatibility and loading, `.env`, required settings, AWS credential source (Bedrock only; no AWS call) and index names. Offline by default; `--check-opensearch` (read-only; also reports the neural-search (AI Search in the Instaclustr console) and ISM plugins), `--check-embeddings` and `--check-ner` add live checks. Never prints a setting or credential value. |
| [Setup task](../tasks/setup-environment.md) | Teaching sequence and setup steps. |
| [Infrastructure choices](../concepts/INFRASTRUCTURE_CHOICES.md) | What runs where, and why the lab uses each service. |
| [Troubleshooting](../troubleshooting/setup-environment.md) | Configuration, spaCy model, and certificate failures. |
