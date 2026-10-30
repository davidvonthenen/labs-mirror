[Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md)

# Baseline Vector RAG reference and source map

## Commands

Run from `scripts/baseline-vector-rag/` with the lab's `.venv` active.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make ingest` | `python -m src.ingest` | Embeds the 260-recipe sample and indexes it into `recipes-vector-baseline`. |
| `make query QUESTION="..."` | `python -m src.query --question "..."` | Runs one kNN query and one LLM answer. Without `--question`, `src.query` asks both Fish and Chips questions. |
| `make validate` | `python -m src.validate` | Smoke test; prints two `[PASS]` lines (hits and an answer came back; the documented retrieval gap reproduced). |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/baseline-vector-rag/Makefile) | Install, ingest, query, validate, and clean targets. |
| [`src/__init__.py`](../scripts/baseline-vector-rag/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/ingest.py`](../scripts/baseline-vector-rag/src/ingest.py) | `build_text()` renders each recipe (cautions spelled out in plain English); `main()` embeds with `EmbeddingModel`, creates the index with `ensure_recipe_vector_index()`, and indexes every recipe. |
| [`src/query.py`](../scripts/baseline-vector-rag/src/query.py) | `ask()` embeds the question, runs `recipe_knn_search()`, builds the context with `_build_context()`, and calls `generate()` under a "say I don't know" `SYSTEM_PROMPT`. |
| [`src/validate.py`](../scripts/baseline-vector-rag/src/validate.py) | `test_baseline_vector_rag_returns_hits_and_answer()` asserts a non-empty answer and at least one hit; `test_baseline_vector_rag_drops_the_named_recipe()` asserts that the thegratefulgirlcooks.com question retrieves the womensweeklyfood.com.au recipe and not its own. |
| [`support/recipes/recipes_sample.json`](../support/recipes/recipes_sample.json) | The curated 260-recipe sample, including the ambiguous Fish and Chips pair. Shared by every task's `ingest.py`; the path is `RECIPES_SAMPLE_PATH` in `support/common/config.py`. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `create_client()`, `ensure_recipe_vector_index()`, `recipe_knn_search()`, `normalize_hits()`. |
| [`support/common/embeddings.py`](../support/common/embeddings.py) | `EmbeddingModel` and `to_list()`. |
| [`support/common/llm_client.py`](../support/common/llm_client.py) | `generate()` for Bedrock or an OpenAI-compatible endpoint. |
| [Embedding models](../concepts/EMBEDDING_MODELS.md), [Chunking](../concepts/CHUNKING.md) and [BM25 versus kNN](../concepts/BM25_VS_KNN.md) | Concept pages with live probes behind this task. |
| [Troubleshooting](../troubleshooting/baseline-vector-rag.md) | Setup and runtime failures for this task. |
