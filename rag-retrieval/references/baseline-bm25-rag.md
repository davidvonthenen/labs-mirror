[Task 3: Baseline BM25 RAG](../tasks/baseline-bm25-rag.md)

# Baseline BM25 RAG reference and source map

## Commands

Run from `scripts/baseline-bm25-rag/` with the lab's `.venv` active.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make ner` | `python -m src.ner_service` | Starts the local spaCy NER service on `127.0.0.1:8000` (`PORT` overrides). Stays in the foreground; Hybrid RAG (NER + Rerank), Governance Tiering and Operationalize Production reuse it. |
| `make ingest` | `python -m src.ingest` | Tags each recipe with NER entities plus its `source`/`recipe_name` and indexes it into `recipes-bm25-baseline`. |
| `make query QUESTION="..."` | `python -m src.query --question "..."` | Runs one BM25 + entity query and one LLM answer. Without `--question`, `src.query` asks both Fish and Chips questions. |
| `make validate` | `python -m src.validate` | Smoke test; prints two `[PASS]` lines (NER service reachable; both Fish and Chips recipes retrieved for both questions). |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/baseline-bm25-rag/Makefile) | Install, NER service, ingest, query, validate, and clean targets. |
| [`src/__init__.py`](../scripts/baseline-bm25-rag/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/ner_service.py`](../scripts/baseline-bm25-rag/src/ner_service.py) | Flask app exposing `POST /ner`; `load_spacy()` loads `en_core_web_sm` (or `SPACY_MODEL`), `_extract_entities()` filters to `DEFAULT_INTERESTING_ENTITY_TYPES`. |
| [`src/ingest.py`](../scripts/baseline-bm25-rag/src/ingest.py) | `build_text()` and `main()`: NER-tags each recipe through `NERClient`, folds in `source` and `recipe_name`, and indexes with `ensure_recipe_bm25_index()`. |
| [`src/query.py`](../scripts/baseline-bm25-rag/src/query.py) | `ask()` extracts question entities with `NERClient`, runs `bm25_search()`, and calls `generate()`. |
| [`src/validate.py`](../scripts/baseline-bm25-rag/src/validate.py) | `test_baseline_bm25_rag_ner_service_is_running()` fails if the NER service is down; `test_baseline_bm25_rag_retrieves_both_same_named_recipes()` asserts both same-named recipes are among the hits, and an answer is returned, for both questions. |
| [`support/recipes/recipes_sample.json`](../support/recipes/recipes_sample.json) | The curated 260-recipe sample. Shared by every task's `ingest.py`; the path is `RECIPES_SAMPLE_PATH` in `support/common/config.py`. |
| [`support/common/ner_client.py`](../support/common/ner_client.py) | `NERClient.extract_entities()`; returns an empty list on any service failure. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `ensure_recipe_bm25_index()`, `bm25_search()`, `build_recipe_bm25_query()`, `normalize_hits()`. |
| [BM25 versus kNN](../concepts/BM25_VS_KNN.md) and [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md) | Concept pages with live probes behind this task. |
| [Troubleshooting](../troubleshooting/baseline-bm25-rag.md) | Setup and runtime failures for this task, including a stopped NER service. |
