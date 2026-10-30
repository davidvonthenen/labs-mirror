[Task 5: Hybrid RAG (NER + Rerank)](../tasks/hybrid-rag-ner-rerank.md)

# Hybrid RAG (NER + Rerank) reference and source map

## Commands

Run from `scripts/hybrid-rag-ner-rerank/` with the lab's `.venv` active and Baseline BM25 RAG's NER service running.

| Make target | Equivalent command | What it does |
|---|---|---|
| `make install` | `python -m pip install -r ../../support/requirements.txt` | Installs the shared dependency set. |
| `make ingest` | `python -m src.ingest` | Writes every recipe to both `recipes-bm25` and `recipes-vector` with the same structured metadata and `tier="hot"`. |
| `make query QUESTION="..." ARGS="--exclude allergen=Sulfites"` | `python -m src.query --question "..." --exclude allergen=Sulfites` | Runs BM25 grounding with hard filters, then vector refinement, then one LLM answer. `--exclude` / `--require` repeat. |
| `make validate` | `python -m src.validate` | Smoke test; prints a `[PASS]` line for retrieval and one for the sulfite exclusion guarantee. |
| `make clean` | — | Removes `__pycache__` and `.pytest_cache`. |

Filter aliases accepted by `--exclude` / `--require` (`FILTERABLE_FIELDS`): `caution` and `allergen` → `cautions`, `diet` → `diet_labels`, `health` → `health_labels`, `cuisine` → `cuisine_type`, `meal` → `meal_type`, `dish` → `dish_type`.

## Source map

These are the implementation files used by this task. When the overview and code differ, follow the current code and rerun the relevant check.

| File | Responsibility |
|---|---|
| [`Makefile`](../scripts/hybrid-rag-ner-rerank/Makefile) | Install, ingest, query, validate, and clean targets. |
| [`src/__init__.py`](../scripts/hybrid-rag-ner-rerank/src/__init__.py) | Makes `src` a package so modules run with `python -m src.<module>`. |
| `src/common` | Symbolic link to the shared [`support/common/`](../support/common/__init__.py) package. |
| [`src/ingest.py`](../scripts/hybrid-rag-ner-rerank/src/ingest.py) | `build_text()`, `_shared_fields()`, and `main()`: one document per recipe written to both indexes; `DEFAULT_TIER = "hot"` is a placeholder Governance Tiering replaces. |
| [`src/query.py`](../scripts/hybrid-rag-ner-rerank/src/query.py) | `ask()` returns `(answer, grounding_hits, refined_hits)`: `bm25_search()` grounds with filters, `recipe_knn_search()` refines restricted to the grounding IDs. |
| [`src/validate.py`](../scripts/hybrid-rag-ner-rerank/src/validate.py) | `test_hybrid_rag_ner_rerank_returns_hits_and_answer()` (fails if the NER service is down; both Fish and Chips recipes must be grounded) and `test_hybrid_rag_ner_rerank_hard_exclusion_filter_is_never_violated()`. |
| [`support/recipes/recipes_sample.json`](../support/recipes/recipes_sample.json) | The curated 260-recipe sample (201 recipes carry a Sulfites caution). Shared by every task's `ingest.py`; the path is `RECIPES_SAMPLE_PATH` in `support/common/config.py`. |
| [`support/common/opensearch_client.py`](../support/common/opensearch_client.py) | `ensure_recipe_bm25_index()`, `ensure_recipe_vector_index()`, `parse_field_value_pairs()`, `build_recipe_bm25_query()`, `build_recipe_vector_query()`, `bm25_search()`, `recipe_knn_search()`. |
| [`support/common/ner_client.py`](../support/common/ner_client.py) | `NERClient` for question and recipe entities. |
| [BM25 versus kNN](../concepts/BM25_VS_KNN.md#probe-3-a-post-filter-over-a-small-pool), [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md) and [Choosing a retrieval approach](../concepts/CHOOSING_RETRIEVAL.md) | The post-filter probe, what NER does here, and where filters fit. |
| [Troubleshooting](../troubleshooting/hybrid-rag-ner-rerank.md) | Setup and runtime failures for this task. |
