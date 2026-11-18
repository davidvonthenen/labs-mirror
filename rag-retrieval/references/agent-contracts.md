# Contracts for coding agents

Use [AGENTS.md](../AGENTS.md) for repository guidance and
[lab.json](lab.json) for task discovery. [insta.yaml](../insta.yaml) declares
operations for the existing Go CLI. The manifest describes possible effects;
it does not authorize actions against an unspecified service. The source maps
in each task reference identify the implementation behind its teaching claims.

## Verification and output

| Check | Execution context | What a pass establishes |
| --- | --- | --- |
| `python scripts/validate_lab.py` | Lab root; dependencies in `support/requirements-test.txt` | Offline document, metadata, path, and source consistency. No external service readiness or retrieval-quality claim. |
| `python support/validate_setup.py` | Lab root; selected runtime environment | Configured local workspace checks. Inspect PASS/FAIL/WARN/SKIP lines and any explicitly requested probes. |
| `python -m src.validate` | Selected `scripts/<task-id>/` directory | That task's assertions on the configured services and sample. Read its external effects before running it. |
| `python -m src.policy_registry` | `scripts/operationalize-production/` | Local policy-history thresholds agree with the selected configuration. Does not deploy policy. |

The offline check returns a JSON result and a nonzero status for failure. The
retrieval, ingestion, monitoring, and live validation modules retain their
human-readable output and third-party dependencies. They have no common JSON
error envelope or `--json` option. Do not parse an LLM's answer as a success
receipt or infer service health from an offline pass.

Direct task scripts normally exit `0` on completion, `1` for an unhandled
exception/assertion, and `2` for argparse usage errors. Make returns its own
failure status. Baseline validators intentionally preserve the sample's
demonstrated weaknesses; they are not general-purpose quality benchmarks.
Assertions must remain enabled: do not set `PYTHONOPTIMIZE` or use `python -O`.

## Working directories and configuration

Each task is a separate `src` package. Execute its module from the directory in
the manifest; do not import several task packages as `src` into one persistent
Python process. Use an explicit subprocess argv and cwd for user-supplied text.
Make's `QUESTION` and `ARGS` values are shell text, not a safe argument-escaping
interface. `ARGS` is forwarded by `query` and the production `monitor`/`policy`
targets; ingestion and validation targets do not forward it.

Queries without `--question` run the built-in demonstration question(s) and
exit. They do not start a conversation loop. The baseline vector task uses
`RAG_TOP_K` and `RAG_NUM_CANDIDATES`; other tasks have their own parser defaults.
Read the selected `query.py` before assuming the same option names everywhere.

The ZIP contains real, matching copies of `common` under all six components.
[support/common/](../support/common/__init__.py) is canonical; mirror shared
edits into `scripts/*/src/common/`. The common configuration resolves the lab
root so corpus paths and the project cache remain valid from each task directory.
The offline checker detects divergence between these copies.

[Settings](../support/common/config.py) reads the first available cwd `.env`
or lab-root `.env`; existing process environment wins. Native tasks do not load
`support/.env` as a separate fallback. Compose reads that file and injects
values into its container. `insta` resolves the managed project's configuration
and passes it to task processes; see [INSTA.md](../INSTA.md). Configuration
comes from the selected execution mode, so a missing dotenv file alone does
not establish missing credentials. Never display secret values while diagnosing.

`Settings`, the supplied dotenv examples, Compose defaults, and the lab's
managed manifest select `LLM_PROVIDER=openai` by default. Accepted alternatives
are `anthropic` and `bedrock`. OpenAI uses `OPENAI_API_KEY`, `OPENAI_MODEL`, and
`OPENAI_BASE_URL`; direct Anthropic uses `ANTHROPIC_API_KEY` and
`ANTHROPIC_MODEL`; Bedrock uses `BEDROCK_MODEL_ID`, `AWS_REGION`, and AWS
credentials/profile resolution. API-key providers do not require AWS credentials.
Set the provider deliberately and use its actual model identifier and credentials.

The shared `generate()` interface is unchanged. OpenAI uses `openai.OpenAI`;
the two Claude paths use `anthropic.Anthropic` and `anthropic.AnthropicBedrock`
respectively. Both Claude clients call `messages.create()` with a top-level
system prompt when supplied, return text blocks, and fail when no text is
returned. All providers use a 60-second timeout and disable SDK retries.
`BEDROCK_MAX_TOKENS` remains the shared budget despite its historical name.
Unset/blank `BEDROCK_TEMPERATURE` omits temperature; an explicit numeric value
is sent only as a user-selected option for a model supporting that parameter.
Anthropic sends this optional value through `extra_body`.

Lab provider selection does not implement new providers in the external `insta`
summary/conversation engine or change its coding backend. See [INSTA.md](../INSTA.md)
for that boundary. Current provider checks are recorded in
[VALIDATION_REPORT.md](../VALIDATION_REPORT.md); local SDK tests do not establish
live account/model access.

An embedding model is local Sentence Transformers inference; generation uses
the selected remote LLM endpoint. Importing common helpers can create local
cache directories and configure certificate bundles; loading an uncached
embedding model can download weights.

The NER service is started from `scripts/baseline-bm25-rag/` using
`python -m src.ner_service`. Tasks 3, 5, and 6 share that service. Its default
endpoint is `http://127.0.0.1:8000/ner`. Use the task's setup instructions or the
managed service declaration; do not assume every component owns a server module.
This server reads `PORT` and `SPACY_MODEL` directly from its process environment,
without loading dotenv. The managed CLI supplies its declared service settings.

## Chunk and corpus contract

The loader accepts a JSON array of recipe objects. `recipe_id`, `recipe_name`,
and `source` are required by direct field access. Ingredient and label fields
are lists. [chunking.py](../support/common/chunking.py) joins the bundled
synthetic sidecar's `recipes` mapping by `recipe_id`, then builds these sections:
ingredients, allergen cautions/labels, optional introduction, method step groups,
and optional serving/storage notes. Ingredients and safety labels remain the
original facts; introduction, method, and notes are synthetic teaching material.

| Item | Current contract |
| --- | --- |
| Parent identity | `recipe_id` identifies the original recipe throughout grounding, refinement, grouping, and audit metadata. |
| Indexed document identity | `chunk_id` is `<recipe_id>-<zero-based ordinal padded to two digits>`. It is used as the OpenSearch `_id`. IDs are positional, not content hashes. |
| Chunk content | `chunk_index`, `chunk_count`, `section`, and `text`; text begins with recipe name, source, and section. |
| Filter metadata | Parent fields, normalized labels, and task-specific fields such as `entities` and `tier` are copied onto each indexed chunk. Both indexes in two-phase tasks must agree. |
| Chunk sizing | `STEPS_PER_CHUNK=3`, `MAX_INGREDIENT_CHARS=700`. These are code constants, not environment variables or CLI flags. Character packing is not a tokenizer-enforced bound; long individual lines or synthetic sections can still exceed a model's token limit. |
| Overfetch | `chunk_fetch(n)` requests `n * CHUNK_OVERFETCH`, currently six chunks per desired recipe. This heuristic does not guarantee `n` distinct recipes. |
| Grouped evidence | `group_by_recipe()` ranks parents by their best chunk score, retains up to `MAX_EVIDENCE_CHUNKS=3` matching chunks in reading order, and exposes `matched_chunks` and combined evidence `text`. |
| Original fixture | 260 recipes become 1,823 chunks with the supplied corpus and sidecar. These counts are not acceptance targets for a different corpus. |

`build_text()` supplies recipe facts to NER; it is no longer the embedding
payload. Vector-capable ingestors embed each chunk's `text` independently;
the BM25-only ingestor does not generate embeddings. Grouped hits carry the
selected chunks and parent metadata. Query context builders also
truncate combined evidence text, so a selected chunk ID does not establish
that every character of that chunk reached the model.

The `--data-file` ingestion option changes the recipe input. It does not select
a synthetic sidecar, change chunking policy, or convert arbitrary PDFs/CSV
files. `load_recipes()` has a Python `synthetic_file` parameter, but ingestion
CLIs expose no corresponding flag. When adapting a corpus, explicitly adapt
the loader/sidecar policy and provenance; reusing a sample recipe ID can attach
sample synthetic passages to unrelated data.

## Index lifecycle and other mutations

Configure the same index settings for ingestion and queries. Ingestion-only
`--index-name`, `--bm25-index-name`, and `--vector-index-name` overrides affect
that invocation, not later queries. Changing an embedding model requires a
compatible index dimension and a deliberate reindexing plan.

**Default ingestion can delete an old-layout index.** All five retrieval
ingestors pass `replace_old_layout=True` into the relevant index helpers. An
existing mapping without `chunk_id` is treated as the older recipe layout and
deleted before replacement. The existing `--recreate` option also deletes
selected index(es), even when they already hold chunks. A query's ensure helper
does not perform this conversion; it rejects an old layout rather than deleting
it. Inspect the selected resource and the authorized scope before ingestion.

For an already chunked index, ingestion upserts matching positional chunk IDs.
It does not delete removed parents or leftover chunks after a recipe shrinks.
Existing chunk mappings are not automatically migrated or validated against a
changed embedding dimension. Use an isolated compatible index or a deliberate
rebuild when changing corpus structure, model, or chunk boundaries. Two-index
ingestion is not transactional.

| Shared resource | Actual behavior |
| --- | --- |
| Task 5 / Task 6 recipe indexes | NER/rerank ingestion assigns every chunk `tier="hot"`; governance ingestion applies its caution-based hot/lt assignment. Running Task 5 ingestion later overwrites Task 6's tier policy. |
| `hybrid-search-pipeline` | Native hybrid ingestion writes its weights; a query ensures the same fixed pipeline with helper defaults, currently BM25/vector `0.5`/`0.5`. Index suffixes do not isolate it. |
| Governance audit index | Query execution attempts an audit write. A returned answer is not proof of successful persistence. |
| `recipes-audit-log-retention` | Production validation may create/attach an ISM policy. It deletes the whole audit index by index age, not individual records by timestamp. An already old index can become immediately eligible. |

`make clean` removes local bytecode/test caches. It is not OpenSearch cleanup.
For managed resources use the existing CLI lifecycle; ownership and attached
cluster handling are described in [INSTA.md](../INSTA.md).

## Retrieval interfaces and limits

| Component | `ask()` return | Meaning of hit lists |
| --- | --- | --- |
| [Baseline vector](../scripts/baseline-vector-rag/src/query.py) | `(answer, hits)` | Parent recipes grouped from normalized kNN chunk hits. |
| [Baseline BM25](../scripts/baseline-bm25-rag/src/query.py) | `(answer, hits)` | Parent recipes grouped from BM25 chunk hits, with a local NER boost. |
| [Native hybrid](../scripts/hybrid-rag-native/src/query.py) | `(answer, hits)` | Grouped normalized parent hits after server-side lexical/vector fusion. No raw `_source`/`_score` return contract. |
| [NER/rerank](../scripts/hybrid-rag-ner-rerank/src/query.py) | `(answer, grounding_hits, refined_hits)` | Filtered BM25 parents, followed by kNN restricted to candidate parent IDs. |
| [Governance](../scripts/governance-tiering/src/query.py) | `(answer, grounding_hits, refined_hits, governance_meta)` | Hot-first grounding and escalation, constrained refinement, named-source pinning, citation handles, and audit metadata. |

The parameters operate at different levels:

| Parameter | Unit and behavior |
| --- | --- |
| Query `top_k` / baseline `RAG_TOP_K` | Requested parent-recipe limit after grouping. Overfetch can still yield fewer distinct parents; governance's named-source pinning can append extra evidence afterward. |
| Two-phase `--candidate-k` | Parent-recipe limit for BM25 grounding after grouping; the BM25 request fetches six times that many chunks. |
| Baseline vector `RAG_NUM_CANDIDATES` | Raw chunk candidate-pool setting. Its effective ANN pool is `max(RAG_NUM_CANDIDATES, chunk_fetch(top_k))`, not a requested count of unique parents. |
| `TIER_MIN_HOT_CANDIDATES` | Minimum count of grouped hot-tier recipes used in escalation decisions. |
| `TIER_MIN_HOT_SCORE` | Score threshold compared with the best hot recipe's best chunk score; it is not a recipe count or calibrated confidence probability. |

For two-phase refinement, `ann_pool_for_filtered_search()` requests a raw
chunk ANN pool of at least 300 and, when counting succeeds, at least the index's
chunk count. This fits the teaching corpus; corpus growth requires reconsidering
cost, engine limits, and retrieval strategy. It is not an unbounded production
guarantee of exhaustive filtered retrieval.

Explicit `--exclude`/`--require` filters in Tasks 5 and 6 constrain stored
metadata. NER does not translate a natural-language exclusion into these flags.
Aliases and normalization are defined in
[opensearch_client.py](../support/common/opensearch_client.py). Unknown fields
pass through rather than being rejected by an allowlist. Repeated requirements
are ANDed in BM25 and OR-per-field in vector filtering; candidate IDs preserve
the BM25 qualification only while both indexes remain consistent.

Governance can append a grounded named-source recipe missing from refinement;
the final evidence count can exceed `top_k`. `[R#]` handles identify retrieved
parents for that answer, and `evidence_chunk_ids` identifies their selected
passages. Citation validation detects unknown handles, not unsupported claims
or missing citations; an answer with no handles can have `citations_valid=True`.
Invalid citation membership is observed, not blocked. Tiers are logical labels,
not storage placement. These constraints remain described in
[SAFETY_AND_SCOPE.md](SAFETY_AND_SCOPE.md).

The refinement stage is another OpenSearch vector query. There is no
cross-encoder, autonomous runtime agent loop, MCP integration, or persistent
conversation memory in this workshop. Coding tools orchestrate the existing
pipeline commands; they do not change those capabilities by reading AGENTS.md.

## Failure interpretation

- BM25 and recipe-kNN transport errors can become empty hits with `_error`;
  `normalize_hits()` does not preserve that error marker. A generated answer
  and process exit `0` can therefore accompany failed retrieval.
- NER extraction can log a request failure and return no entities. Explicit
  metadata filters still apply, but entity boosting is lost.
- Audit writes are best effort. Check stored records when persistence is part
  of the acceptance criterion.
- Audit statistics can contain an `error` alongside zeroed counts. The current
  monitor does not treat that error as an alert by itself; inspect it before
  accepting “No governance alerts.” An empty window is not proof of health.
- Production validation may print an ISM `SKIP` and still exit `0`. Record that
  as an unverified retention result, not a deployment success.

The prebuilt Compose image contains its own checkout and reports container
liveness. It does not automatically include local edits or establish OpenSearch,
LLM, or NER readiness. Verify an adaptation against the actual edited source.
