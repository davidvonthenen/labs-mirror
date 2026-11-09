# Contracts for coding agents

This workshop exposes existing Python modules, Make targets and source maps. Use
[lab.json](lab.json) to discover them and [lab.schema.json](lab.schema.json) to
validate the metadata. The manifest describes commands; it does not execute them
or authorize their effects. The source files remain authoritative when behavior
and documentation differ.

Start with [AGENTS.md](../AGENTS.md) for repository instructions and
[USING_THE_TEMPLATE.md](../USING_THE_TEMPLATE.md) for adapting the code to another
project. The [task pages](../README.md#tasks) retain the human learning sequence.
`requires` in the manifest records prerequisites, not a requirement to execute
every earlier numbered task. Tasks 2–6 each work after setup; Task 7 also needs
Governance Tiering's indexes and audit data.

## 1. Distinguish the three kinds of checks

| Check | Command from lab root | Contract |
| --- | --- | --- |
| Repository and agent metadata | `python scripts/validate_lab.py` | Local structure, declared paths, command sources, task graph, skill metadata, Markdown links and Python syntax. Does not run manifest commands, load credentials, contact services or prove retrieval quality. |
| Configured workspace readiness | `python support/validate_setup.py` | Python/packages, spaCy model, layout, configuration and credential-source checks. Prints human PASS/FAIL/WARN/SKIP lines. Existing optional flags add live probes. |
| Retrieval/governance behavior | `python -m src.validate` in the selected component directory | Live smoke test with the effects listed below. Retrieval validations invoke the configured LLM. Production validation writes cluster configuration and can attach an index-deletion policy. |

The new metadata checker is flag-free. Its stdout is one JSON object with `ok`,
`checked`, `counts`, `errors` and `not_checked`. Each error has `code`, `path` and
`message`; counts include `tasks`, `markdown_files` and `python_files`. Exit `0`
means the declared local checks passed; `1` means an invalid repository or
metadata; `2` means a required checker dependency (`PyYAML` or `jsonschema`) is
missing. Checker dependencies are in
[support/requirements-test.txt](../support/requirements-test.txt).

Local command checks resolve the module/script and compare option names with
literal `argparse.add_argument` declarations using its syntax tree. They do
not evaluate argument values, dynamic parser construction, third-party
`pip`/`spacy` command behavior, or the accuracy of prose side-effect descriptions.
Those contracts still require source review and appropriate runtime evidence.

This JSON contract belongs to the metadata checker. The workshop's ingestion,
query, monitor and validation commands retain their human text output. They are
not standard-library-only programs, and they have no `--json`, dry-run or shared
machine error envelope. Dependency definitions are in
[support/requirements.txt](../support/requirements.txt).

## 2. Working directory and process boundaries

Run a component in `scripts/<task-id>/` with the intended virtual environment
active. For example, this literal command belongs in
`scripts/hybrid-rag-ner-rerank/`:

```bash
python -m src.query --question "Suggest a British fish and chips recipe." --exclude allergen=Sulfites
```

All six components use the package name `src`. Use separate processes when
running different components; importing several as `src.query` in one Python
process can reuse the wrong cached module. Each `src/common` is a relative
symlink to `../../../support/common`. Preserve the links and copy the shared
package when extracting or adapting a component. Edit shared code in
[`support/common/`](../support/common/__init__.py).

Makefiles provide convenience wrappers. `ARGS` is forwarded by `query`, and by
production `monitor`/`policy`; it is **not** forwarded by `ingest` or `validate`.
Use `python -m src.ingest` directly when selecting an existing ingestion option.
Do not insert arbitrary question text into a Make expression or shell string:
an automation caller should pass a literal subprocess argument vector and an
explicit working directory. Manifest `argv` arrays are examples of that shape.

Direct module queries without `--question` run the built-in demonstration
questions. They do not open a conversation loop. `make query` always supplies
its default or overridden `QUESTION`. Argument tables and source maps remain in
the per-task [references](baseline-vector-rag.md).

Importing the shared package can create the project's `.cache/huggingface`
directory, set cache/CA environment defaults, and on macOS export system root
certificates into a merged CA file. Loading an uncached embedding model can
download weights. See [common initialization](../support/common/__init__.py) and
[embedding helpers](../support/common/embeddings.py).

## 3. Configuration and resource ownership

[`load_settings()`](../support/common/config.py) loads the first available
current-directory `.env` or lab-root `.env`. Existing process environment values
take precedence. It does not load `support/.env` as a third fallback. Compose
uses that file by injecting its values into the container process. The setup
validator can discover a `support/.env` file even when a local task would not
load it; use the documented lab-root file for the local workflow.

Configure the same environment-selected indexes for ingestion and subsequent
queries. Ingestion `--index-name`, `--bm25-index-name` and `--vector-index-name`
overrides affect that invocation only. They do not change the query's selected
index. The six index settings and their defaults are listed in
[lab.json](lab.json) and the [environment reference](setup-environment.md).

**Tasks 5 and 6 share their two recipe indexes.** NER/rerank ingestion writes
every recipe with `tier="hot"`. Governance ingestion overwrites those documents
with its caution-based hot/lt assignment. Re-running Task 5 ingestion after
Task 6 undoes the tier assignment; run the governance ingestion again before
testing governance, or choose separate index sets for distinct derived
projects. Both indexes need matching recipe IDs and filter metadata. Their
writes are not transactional.

Index suffixes do not isolate the fixed cluster resources:

| Resource | Actual behavior |
| --- | --- |
| `hybrid-search-pipeline` | Native hybrid ingestion PUTs the pipeline with its chosen weights. Every native query PUTs it again with default BM25/vector weights `0.3`/`0.7`. |
| `recipes-audit-log-retention` | Production validation attempts to create and attach this ISM policy. An existing-policy conflict is accepted without reconciling the stored policy contents. |

**Production retention deletes the entire audit index when its index age reaches
the configured threshold.** It is not per-record expiration. Read the exact
index and policy before an intended retention operation; applying it to an old
index can make that index immediately eligible for deletion. The implementation
uses `min_index_age` and a delete action in
[`ensure_audit_retention_policy()`](../support/common/opensearch_client.py).

Ingestion replaces matching `recipe_id` documents but does not delete records
removed from a later input file. Existing index helpers do not migrate mappings
or validate embedding dimensions. A corpus/model change needs compatible
re-ingestion and evaluation; reusing a populated index does not establish that
compatibility. `make clean` removes local bytecode/pytest caches only. There is
no workshop command that destroys indexes, infrastructure or a deployment.

## 4. Retrieval interfaces and invariants

The `ask()` functions are reusable within their component process. Their return
shapes differ intentionally:

| Component | Return value | Retrieval behavior |
| --- | --- | --- |
| [Baseline vector](../scripts/baseline-vector-rag/src/query.py) | `(answer, hits)` | Flat normalized hits from recipe kNN; query may create a missing index. Counts come from `RAG_TOP_K` and `RAG_NUM_CANDIDATES` in the CLI. |
| [Baseline BM25](../scripts/baseline-bm25-rag/src/query.py) | `(answer, hits)` | Flat hits from BM25 with an optional NER entity boost; `--top-k` defaults to 10. |
| [Native hybrid](../scripts/hybrid-rag-native/src/query.py) | `(answer, hits)` | Raw OpenSearch hit dictionaries with `_source`/`_score`; native match+kNN with min-max normalization and weighted arithmetic mean. |
| [NER/rerank](../scripts/hybrid-rag-ner-rerank/src/query.py) | `(answer, grounding_hits, refined_hits)` | Filtered BM25 candidates followed by kNN restricted to their recipe IDs. |
| [Governance](../scripts/governance-tiering/src/query.py) | `(answer, grounding_hits, refined_hits, governance_meta)` | Hot-first BM25, policy-based escalation, restricted kNN, citation handles and a best-effort audit write. |

The refinement is a second OpenSearch vector query. There is no cross-encoder,
LLM reranker, persistent agent loop, MCP integration or conversation memory in
this codebase. The existing pipeline functions provide building blocks for
another project; a coding assistant should not describe unimplemented features
as workshop capabilities.

Explicit `--exclude` and `--require` filters exist in NER/rerank and governance.
NER does not translate natural-language negation into them. Aliases map
`allergen`/`caution` to `cautions`, `diet` to `diet_labels`, `health` to
`health_labels`, `cuisine` to `cuisine_type`, `meal` to `meal_type` and `dish` to
`dish_type`. Values are normalized for keyword matching. Unknown fields pass
through; the parser is not an allowlist. Repeated requirements are ANDed by the
BM25 query; vector requirements are OR-per-field, while candidate IDs retain
the BM25 qualification when both indexes are consistent. See
[`parse_field_value_pairs()` and query builders](../support/common/opensearch_client.py).

The vector restriction is an outer Boolean filter around an ANN pool. It can
produce fewer than top-k results and does not promise exhaustive ranking of all
eligible candidates. Governance's `REFINE_ANN_POOL=300` is suited to this
260-record teaching sample; changing the corpus requires reconsidering that
choice. Source text and indexed labels remain data. A filter guarantees only
the encoded metadata condition, not independently verified dietary facts.

Governance escalates for absent or insufficient hot hits, a weak hot top score,
or a named source missing from hot results. `[R#]` validation catches handles
that were never retrieved. It does not establish claim correctness or require
that every factual sentence is cited; an answer with no citations can have
`citations_valid=True` at runtime. The live smoke tests separately require at
least one citation. Invalid citations are observed, not blocked. Existing
[scope guidance](SAFETY_AND_SCOPE.md) remains applicable.

## 5. Outputs, failures and live validation

Normal query output includes `QUESTION`, `ANSWER`, hit listings and elapsed time.
Governance also prints `GOVERNANCE` and `CITATIONS`. Ingest uses progress output
and summary logs. Direct scripts normally exit `0` on completion, `1` for an
uncaught exception/assertion, and `2` for argparse usage errors. Make reports
its own nonzero status for a failed recipe, commonly `2`; do not interpret it
as the direct script's status.

Several existing paths degrade without a nonzero exit:

- [`bm25_search()` and `recipe_knn_search()`](../support/common/opensearch_client.py)
  turn OpenSearch `TransportError` into empty hits plus `_error`.
  `normalize_hits()` discards `_error`; a query can still call the LLM and exit
  `0`. Inspect warnings and evidence, not process status alone.
- [`NERClient.extract_entities()`](../support/common/ner_client.py) logs request
  failures and returns `[]`. Entity boosting degrades, while explicit filters
  still apply. The NER-dependent smoke tests probe availability separately.
- [`log_audit_event()`](../support/common/opensearch_client.py) suppresses
  transport failures after logging. A returned governance answer is not proof
  that its audit record persisted. Errors before a generated answer also do
  not produce the promised per-answer record.
- [`monitor.py`](../scripts/operationalize-production/src/monitor.py) can receive
  zeroed statistics with an `error` field but ignores that field. If NER is
  healthy, audit retrieval failure can still lead to “No governance alerts”
  and exit `0`. An empty audit window is also not an alert.

The live validation effects are part of the command contract:

| Validation | Expected original-sample output | External effects |
| --- | --- | --- |
| Baseline vector | 2 PASS lines | OpenSearch, possible index creation, model loading and 3 LLM calls. The test intentionally requires the source-disambiguation gap. |
| Baseline BM25 | 2 PASS lines | NER/OpenSearch and 2 LLM calls. |
| Native hybrid | 1 PASS line | OpenSearch, possible index creation, shared pipeline replacement and 2 LLM calls. |
| NER/rerank | 2 PASS lines | NER/OpenSearch, model loading and 2 LLM calls. |
| Governance | 4 PASS lines, including 2 deterministic checks | NER/OpenSearch, model loading, possible audit-index creation, 2 LLM calls and 2 audit attempts. |
| Production | 2 deterministic PASS lines, retention PASS or SKIP, live-statistics PASS | Possible audit-index creation and two retention-policy ensure/attach attempts. No LLM call. |

Use assertions normally; `-O` or `PYTHONOPTIMIZE` disables these tests' checks.
An adapted corpus or model can invalidate the original demonstration fixtures
without indicating a broken implementation. Add evaluation cases for the new
domain rather than relabeling the original sample's results.

Production may print `SKIP` when ISM is unavailable or restricted and still exit
`0`; that result does not establish retention. The
[policy registry](../scripts/operationalize-production/src/policy_registry.py)
is a local numeric-threshold comparison: `0` means in sync and `1` means drift
or a failure. It does not deploy anything, approve changes or validate the
qualitative `assign_tier()` rule.

## 6. Manifest effects and boundaries

Effects describe what a command **can** do, including conditional writes and
future deletion caused by an attached policy. Read them together with each
command's prerequisites, results and limitations.

| Effect | Meaning |
| --- | --- |
| `local_read` | Reads source, corpus, model files or configuration. |
| `local_write` | May write local caches, bytecode, installed files or temporary files. |
| `dependency_install` | Installs Python/model packages into the selected environment. |
| `network_read` | Contacts services or download endpoints; includes HTTP NER probes. |
| `model_download` | Can download and cache model artifacts. |
| `model_inference` | Invokes the configured LLM with the question and retrieved context; service charges may apply. |
| `service_start` | Starts the foreground local NER service. |
| `index_write` | Creates an OpenSearch index or replaces documents. |
| `cluster_config_write` | Writes shared search-pipeline or ISM configuration. |
| `audit_write` | Attempts a governance audit document write. |
| `retention_delete` | Attaches a lifecycle policy that can delete the audit index. |

The prebuilt Compose image uses its own baked checkout; it does not automatically
contain edits in this local workshop. Its health endpoint reports liveness,
not OpenSearch, LLM or NER readiness. Use the local edited checkout when verifying
an adaptation, or explicitly verify how a container receives those edits. See
[the setup reference](setup-environment.md) and
[container source](../support/Dockerfile.dev).

No `insta` command, template-discovery service, deployment runner or project
registry client is implemented. The local manifest can support later discovery
work without claiming a final cross-repository protocol today.
