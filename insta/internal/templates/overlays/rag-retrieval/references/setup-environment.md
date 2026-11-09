[Task 1: Prepare the workspace](../tasks/setup-environment.md)

# Workspace environment reference and source map

## Workspace settings

| Property | Behavior |
| --- | --- |
| Python | A local virtual environment at the lab root (`.venv`). Python 3.12 is the default recommended version. `.python-version` selects 3.12 for tools that read it. Dependencies must also support the chosen interpreter and platform; spaCy 3.8.16 currently declares Python >=3.9,<3.15, so the current dependency set does not promise Python 3.15+ installation. |
| Dependencies | One shared set in `support/requirements.txt`, plus spaCy's `en_core_web_sm` model for the NER service. spaCy is pinned to 3.8.16, the latest stable release verified on October 14, 2026. The official OpenAI Python SDK is included. The obsolete setuptools and click caps are removed, and NumPy 2 is allowed. Download the English model again after upgrading an existing environment. |
| Configuration | One `.env` at the lab root, copied from `support/.env.example`. `load_settings()` uses `_load_env_once()` to load the first file found: `./.env` relative to the current directory, then the lab-root `.env`. Existing environment variables take precedence. |
| OpenSearch | One Instaclustr cluster shared by every task; each task uses its own index names. On a cluster shared with other people, override the six `OPENSEARCH_*_INDEX` names in `.env` with a suffix (see Task 1, step 3). The `hybrid-search-pipeline` search pipeline and the `recipes-audit-log-retention` ISM policy have fixed names and are shared per cluster. |
| LLM | Amazon Bedrock (`LLM_PROVIDER=bedrock`, default) or an OpenAI-compatible endpoint (`LLM_PROVIDER=openai`, via the official OpenAI Python SDK). |
| Local services | The NER service from Baseline BM25 RAG on `127.0.0.1:8000`; no other local service. |
| Writes | `.cache/` at the lab root (merged CA bundle and HuggingFace model cache), OpenSearch indexes on your cluster, and `__pycache__` folders. `.env` and `.cache/` are git-ignored. |
| Working directory | Every task runs from its own `scripts/<task-id>/` directory, with `python -m src.<module>` or `make <target>`. |

Keep `.env` out of anything you share: it holds your OpenSearch password and LLM credentials.

The setup check's `env-file` probe also recognizes `support/.env` and container runtime configuration, but that does not add another dotenv search path. [`support/docker-compose.yaml`](../support/docker-compose.yaml) injects `support/.env` through `env_file`; native task commands should use the lab-root `.env`. A file at `support/.env` is loaded directly only when `support/` is the current directory.

## Source map

| Source | Responsibility |
| --- | --- |
| [`support/.env.example`](../support/.env.example) | Every setting for every task, with placeholders. |
| [`support/requirements.txt`](../support/requirements.txt) | The single shared dependency set. |
| [`support/common/config.py`](../support/common/config.py) | `Settings`, `load_settings()`, and `require_configured()`, which lists missing values. |
| [`support/common/__init__.py`](../support/common/__init__.py) | Merged CA bundle and project-local HuggingFace cache under `.cache/`. |
| [`support/validate_setup.py`](../support/validate_setup.py) | The setup check from Task 1: Python version (recommends 3.12), layout, packages, spaCy model compatibility/loading, `.env`, required settings, AWS credential source (Bedrock only; no AWS call) and index names. Offline by default; `--check-opensearch` (read-only; also reports the neural-search and ISM plugins), `--check-embeddings` and `--check-ner` add live checks. Never prints a setting or credential value. |
| [Setup task](../tasks/setup-environment.md) | Teaching sequence and setup steps. |
| [Infrastructure choices](../concepts/INFRASTRUCTURE_CHOICES.md) | What runs where, and why the lab uses each service. |
| [Troubleshooting](../troubleshooting/setup-environment.md) | Configuration, spaCy model, and certificate failures. |
