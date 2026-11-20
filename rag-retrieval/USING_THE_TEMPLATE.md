# Using this workshop as a project template

Use the seven-task workshop to understand retrieval, then adapt the relevant
source pattern to your own corpus. Human task pages and agent guidance point
to the same Python code. The agent layer adds repository instructions,
machine-readable task discovery, CLI declarations, and offline checks; it
does not replace the human walkthrough or add a second retrieval implementation.

## 1. Open the complete source

Extract the complete `rag-retrieval/` folder, including hidden files. Work in
the directory containing `AGENTS.md`, `README.md`, `scripts/`, and `support/`.
This ZIP contains actual `common` directories under each component, so normal
ZIP extraction does not need symbolic-link support.

For an installed Codex CLI, start from that folder:

```bash
cd rag-retrieval
codex
```

**Expected:** the coding tool opens with the lab as its working directory.
Ask it to read `AGENTS.md` and the task manifest before changing files. Claude
Code has a small [CLAUDE.md](CLAUDE.md) redirect to the same instructions. In
Cursor or another coding tool, explicitly include `AGENTS.md` and the selected
task in your request when automatic discovery has not been established.
No separate Skill installation or MCP connection is required.

For an existing `insta` project, follow [INSTA.md](INSTA.md). The manifest is
compatible with the existing CLI; obtaining this revision through `insta new`
also depends on which lab revision its configured catalog contains. Updating
this ZIP does not publish it to the remote catalog.

Generation defaults to OpenAI-compatible requests. `LLM_PROVIDER=anthropic`
selects the Anthropic API; `LLM_PROVIDER=bedrock` selects Claude through
`anthropic.AnthropicBedrock` with AWS authentication. Supply the matching
credentials/model settings from [the environment reference](references/setup-environment.md).
Changing the lab provider preserves the retrieval tasks and does not extend
the external CLI's own summary/conversation provider implementation.

| Information | Canonical location |
| --- | --- |
| Human journey and task selection | [README.md](README.md), [tasks/](tasks/) |
| Repository instructions for coding tools | [AGENTS.md](AGENTS.md) |
| Task IDs, paths, prerequisites, command argv/cwd, effects, checkpoints | [references/lab.json](references/lab.json) |
| Executable operations and lifecycle for `insta` | [insta.yaml](insta.yaml), [INSTA.md](INSTA.md) |
| Runtime, chunking, output, and mutation contracts | [references/agent-contracts.md](references/agent-contracts.md) |
| Concepts, source maps, and failure diagnosis | [concepts/](concepts/), [references/](references/), [troubleshooting/](troubleshooting/) |
| Offline consistency check | [scripts/validate_lab.py](scripts/validate_lab.py) |
| Agent behavior review cases | [references/agent-evals.md](references/agent-evals.md) |

Read only the context needed for the selected task. Function bodies and
configuration establish behavior; documentation must agree with them.

## 2. Establish a baseline

From the lab root in the chosen Python environment:

```bash
python -m pip install -r support/requirements-test.txt
python scripts/validate_lab.py
```

**Expected:** dependency installation completes, then the checker exits `0`
with an `ok: true` JSON result. Installation may contact the package index;
the checker itself uses local files and does not require workshop credentials.
A failing check identifies inconsistencies to fix before relying on metadata.

For live use, complete [Task 1](tasks/setup-environment.md) or the managed CLI
setup in [INSTA.md](INSTA.md). Then run the selected task's ingestion, queries,
and validation using its stated cwd and prerequisites. Existing ingestion can
replace old-layout indexes, so establish the intended target and resource
ownership before the live run. An offline pass is not a substitute for this.

An adaptation should use the destination requested by the user while keeping
the source workshop available. For a complete copy, from its parent directory:

```bash
test ! -e my-rag-project && cp -R rag-retrieval my-rag-project
```

**Expected:** a new complete folder, including dotfiles, is created only if the
destination does not already exist. `my-rag-project` is an example name. For an
existing repository, follow its workflow and review the diff instead of
introducing an unrelated nested repository.

## 3. Select a retrieval pattern

Read [Choosing retrieval](concepts/CHOOSING_RETRIEVAL.md). The teaching order
does not require every application to implement every stage.

| Application requirement | Starting source | Decision to retain |
| --- | --- | --- |
| Meaning-based passage retrieval | [Baseline Vector RAG](references/baseline-vector-rag.md) | Similarity does not prove source identity or enforce a hard constraint. |
| Exact names, identifiers, and lexical matches | [Baseline BM25 RAG](references/baseline-bm25-rag.md) | This example also uses a local NER boost; its dependencies reflect that. |
| One-index lexical/vector fusion | [Native hybrid](references/hybrid-rag-native.md) | Uses an OpenSearch search pipeline shared across indexes. |
| Structured requirements before semantic refinement | [NER + rerank](references/hybrid-rag-ner-rerank.md) | Both indexes must agree on parent IDs and metadata; refinement is kNN, not a cross-encoder. |
| Priority retrieval with escalation and evidence records | [Governance tiering](references/governance-tiering.md) | Tiers, named-source behavior, audit persistence, and citation limits need explicit evaluation. |
| Monitoring and policy checks | [Production operations](references/operationalize-production.md) | Retention can delete a whole audit index; monitoring output needs interpretation. |

A support-document project with exact error codes and paraphrased symptoms can
start from native hybrid retrieval. If product/version constraints must exclude
other records, evaluate the two-phase filtering pattern against those cases.
Choose using observable requirements and retrieval evidence.

## 4. Adapt the corpus and chunk contract

Establish the target data, representative questions, required output,
destination, resource ownership, and acceptable failure behavior. Use available
files to map the data before asking for missing decisions. Read the selected
`ingest.py`, `query.py`, and `validate.py` with their task/reference pages.

The current loader expects a recipe JSON array. Passing another path to
`--data-file` does not create a generic document loader. Direct field access
requires `recipe_id`, `recipe_name`, and `source`; ingredient/label fields are
lists. The sidecar joins synthetic passages by recipe ID. Choose whether to
map your source to that schema or make a focused loader/schema adaptation.

| Contract to decide | Source to inspect | Required adaptation work |
| --- | --- | --- |
| Parent identity and provenance | Selected ingestor; [chunking.py](support/common/chunking.py) | Define stable parent IDs and source attribution. Avoid sample ID collisions that could attach the bundled synthetic sidecar. |
| Passage boundaries and text | `chunk_recipe()`, `_sections()`, `chunk_prefix()` | Split the target's actual sections, preserve a source-identifying prefix, and check tokenizer limits. Current character/step rules are recipe-specific. |
| Child identity and replacement | `chunk_id`, `_check_layout()` in [opensearch_client.py](support/common/opensearch_client.py) | Positional IDs change meaning when sections move. Define obsolete-chunk removal or rebuild behavior; ordinary upsert leaves stale chunks. |
| Exact metadata and exclusions | [labels.py](support/common/labels.py), index mappings and filter helpers | Define normalized values and missing-field semantics. Copy parent filter fields to every chunk and keep both indexes consistent. |
| Embedding model and dimension | [embeddings.py](support/common/embeddings.py), vector index mappings | Align embedding output and index dimension; use an isolated/rebuilt index for incompatible changes. |
| Retrieval counts and grounding | `chunk_fetch()`, `group_by_recipe()`, selected `ask()` | Distinguish requested chunks from returned parents. Evaluate duplicate-heavy hits and candidate coverage. |
| Evidence and answer context | Selected `_build_context()` and governance metadata | Retain parent/chunk provenance, account for context truncation, and define citation/no-evidence behavior. |
| Resource configuration | [config.py](support/common/config.py), [insta.yaml](insta.yaml) | Align ingestion/query index names and resolve shared pipeline/policy ownership. Do not modify the external CLI. |
| Generation provider | [llm_client.py](support/common/llm_client.py), [environment reference](references/setup-environment.md) | Choose `openai`, `anthropic`, or `bedrock` without changing retrieval task code. Keep optional temperature unset unless supported by the selected model; evaluate answer/citation behavior separately from adapter syntax. |

The supplied `load_recipes()` can accept a `synthetic_file` in Python; the
ingestion CLI has no such flag. Matching sidecar IDs override a record's inline
`synthetic` value. For a new corpus, deliberately replace that teaching-sidecar
policy or its data. Do not represent generated recipe method text as original
source content.

Chunk settings are existing constants, not a new configuration surface.
Preserve the current defaults when the requested adaptation does not require
changing them. If common code changes, edit `support/common/` and synchronize
the corresponding files into all six task copies; the offline checker guards
against drift. Do not introduce a second chunking implementation in a new
wrapper or duplicate complete programs in Markdown.

Keep stable task IDs and paths. Update the affected task, concept/reference,
troubleshooting guide, discovery metadata, and CLI declaration together when
their contract changes. Do not expose a new flag merely to avoid a small,
clear source adaptation.

## 5. Verify the new behavior

Use acceptance cases that describe what must be retrieved, excluded, grouped,
and retained as evidence. Expected answers alone do not establish those facts.

| Case | Evidence to inspect |
| --- | --- |
| Exact identity amid similar text | Correct parent ID and source, with relevant chunk IDs. |
| Paraphrase | Relevant passages retrieved without depending on literal wording alone. |
| Structured exclusion/requirement | Every returned chunk and parent satisfies stored metadata rules. |
| Several high-scoring chunks from one parent | Deduplicated parent results and preserved matching-passage provenance. |
| Empty or failed retrieval | Defined behavior, inspected warnings/errors, and no fabricated evidence claims. |
| Corpus update or shortened document | Removed parents/obsolete chunks are handled by the chosen lifecycle. |
| Governance, if included | Tier/escalation decisions, named-source behavior, permitted citation handles, and persisted audit data where required. |

Run the offline checker after edits, then the relevant runtime checks on the
authorized target. An adapted corpus may invalidate the original Fish and
Chips fixtures without breaking the retrieval implementation; create meaningful
domain-specific cases rather than relabeling those demonstrations as passing.
For an unchanged workshop, preserve the existing teaching assertions.

Separate passed, failed, skipped, and unrun checks. Model wording and score
ordering can vary. A matching citation handle does not prove the sentence it
follows, and a process exit `0` does not prove retrieval or audit success.

## 6. Prompts for coding tools

### Learn and run the current workshop

```text
Read AGENTS.md and references/lab.json. Help me work through the chunked
Baseline Vector RAG task using the existing human instructions. Explain
the parent/chunk relationship, the purpose of each command, and the
evidence needed at the completion checkpoint. Use the current source and
working directories. Inspect the selected cluster/index and the ingestion
effects before running a mutation. Keep the original chunking behavior.
Report checks actually run and distinguish unavailable live checks.
```

### Build for another corpus

```text
Read AGENTS.md, USING_THE_TEMPLATE.md, and references/lab.json. Use this
workshop as the starting point for a project over my supplied support
documents in the destination I specify. Queries include exact error codes
and paraphrased symptoms; product/version requirements must be enforced.
Inspect the data and select the retrieval pattern that fits these cases.

Define parent/chunk identity, provenance, metadata filters, sidecar policy,
update/delete behavior, index names, and measurable acceptance cases.
Implement focused changes in the existing layout and keep shared common
copies synchronized. Preserve the workshop for reference. Do not alter
the insta CLI or add flags unless the implementation requires them.
Run offline checks and relevant available runtime checks; report actual
evidence and remaining prerequisites without inventing passing results.
```

Replace the example corpus, constraints, and destination with the actual
project requirements. These prompts guide the workflow; they do not authorize
use of an unrelated cluster or deployment.

## 7. Completion and publication

Keep the human task format: introduction, learning outcomes and step table,
concept explanation, prerequisites, numbered steps with expected outcomes,
and completion checkpoint. Keep full programs in source files and diagnosis
in `troubleshooting/`. The agent layer should route readers to that material,
not create an independently maintained copy of the workshop.

Review the resulting diff, metadata checks, relevant live evidence, and the
[agent review scenarios](references/agent-evals.md). Exclude credentials,
virtual environments, caches, logs, and managed runtime state from the package.
Include a clear record of implemented behavior and any unrun checks. Remote
catalog publication and client-specific agent evaluation are separate actions;
do not report either as completed because this source archive was updated.
