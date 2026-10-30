[Task 4: Hybrid RAG (Native)](../tasks/hybrid-rag-native.md)

# Hybrid RAG (Native) reference and source map

## Commands

Run from `scripts/hybrid-rag-native/` with the lab's `.venv` active. No NER service is needed.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make ingest` | `python -m src.ingest` | Creates `recipes-hybrid`, (re)creates `hybrid-search-pipeline`, and indexes the sample. `ARGS` is not passed to `ingest`. `python -m src.ingest --bm25-weight 0.5 --vector-weight 0.5` writes those weights, but `query.py` re-creates the pipeline with 0.3 / 0.7 on its next run. |
| `make query QUESTION="..."` | `python -m src.query --question "..."` | Runs one native hybrid query and one LLM answer. Without `--question`, `src.query` asks both Fish and Chips questions. |
| `make validate` | `python -m src.validate` | Smoke test; prints `[PASS] Hybrid RAG (Native) answered both disambiguation questions with hybrid-retrieved evidence`. |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/hybrid-rag-native/Makefile) | Install, ingest, query, validate, and clean targets. |
| [`src/__init__.py`](../scripts/hybrid-rag-native/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/ingest.py`](../scripts/hybrid-rag-native/src/ingest.py) | `build_text()` renders text that is both embedded and BM25-matched; `main()` calls `ensure_hybrid_index()` and `ensure_hybrid_pipeline()` with `--bm25-weight` / `--vector-weight` (defaults 0.3 / 0.7); `query.py` calls `ensure_hybrid_pipeline()` again with the defaults. |
| [`src/query.py`](../scripts/hybrid-rag-native/src/query.py) | `ask()` embeds the question and runs one `hybrid_search()` through the pipeline, then calls `generate()`. |
| [`src/validate.py`](../scripts/hybrid-rag-native/src/validate.py) | `test_hybrid_rag_native_disambiguates_similar_questions()` asserts an answer, and that each question's own recipe is in the top 2 hybrid hits. |
| [`support/recipes/recipes_sample.json`](../support/recipes/recipes_sample.json) | The curated 260-recipe sample. Shared by every task's `ingest.py`; the path is `RECIPES_SAMPLE_PATH` in `support/common/config.py`. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `ensure_hybrid_index()`, `ensure_hybrid_pipeline()`, `hybrid_search()`, `HYBRID_PIPELINE_NAME`. |
| [BM25 versus kNN](../concepts/BM25_VS_KNN.md#hybrid-fusion-and-the-weights-03-and-07) and [Choosing a retrieval approach](../concepts/CHOOSING_RETRIEVAL.md) | The weight sweep behind 0.3 and 0.7, and where hybrid fits among the approaches. |
| [Troubleshooting](../troubleshooting/hybrid-rag-native.md) | Setup and runtime failures for this task. |
