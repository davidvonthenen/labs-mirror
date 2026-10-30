# Using this workshop as a project template

Use the same source to learn how retrieval works, change an existing task, or
build a project for another corpus with a coding assistant. The seven human
tasks remain the teaching path. Agent guidance selects from those tasks and
their source maps; it does not maintain a second implementation.

This guide applies to the complete `rag-retrieval/` folder in this archive.
It adapts the supplied journey-template guidance to the actual recipe workshop,
whose runtime modules use third-party dependencies and human-readable output.

## Contents

- [1. Open the complete local workshop](#1-open-the-complete-local-workshop)
- [2. Check the source package before using services](#2-check-the-source-package-before-using-services)
- [3. Choose a source pattern](#3-choose-a-source-pattern)
- [4. Adapt a project from that pattern](#4-adapt-a-project-from-that-pattern)
- [5. Preserve the human teaching structure](#5-preserve-the-human-teaching-structure)
- [6. Verify the changed behavior](#6-verify-the-changed-behavior)
- [7. Prompts for coding tools](#7-prompts-for-coding-tools)
- [8. Completion and publication](#8-completion-and-publication)
- [Design references and future scope](#design-references-and-future-scope)

## 1. Open the complete local workshop

Extract the archive with hidden files and symbolic links preserved. Keep
`support/common/` and every `scripts/*/src/common` link together. Open the
directory containing `README.md`, `AGENTS.md`, `scripts/`, and `support/` in
your coding tool. Starting a tool above this folder in the larger `labs/`
repository may leave this folder's local guidance undiscovered until it is
explicitly read.

For an installed Codex CLI, from the directory containing the extracted lab:

```bash
cd rag-retrieval
codex
```

**Expected:** Codex opens in the workshop directory. Ask it to identify
`AGENTS.md` and the `instaclustr-rag-builder` Skill before using the workflow.
In Codex, `/skills` or a `$instaclustr-rag-builder` mention selects the Skill.
The local Skill lives at
[`.agents/skills/instaclustr-rag-builder/SKILL.md`](.agents/skills/instaclustr-rag-builder/SKILL.md).
This follows the current [Codex instruction-discovery documentation](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
and [local Skills documentation](https://learn.chatgpt.com/docs/build-skills).

Other coding tools can read the same files directly. Use the portable prompt in
[section 7](#7-prompts-for-coding-tools); automatic discovery depends on the
client. Uploading a ZIP alone is not evidence that its instructions activated.

### Where each kind of information lives

| Artifact | Purpose |
| --- | --- |
| [README.md](README.md) and [tasks/](tasks/) | Human journey, learning outcomes, steps, and observed checkpoints. |
| [AGENTS.md](AGENTS.md) | Repository rules, source authority, routing, and verification boundaries. |
| [Local Skill](.agents/skills/instaclustr-rag-builder/SKILL.md) | Workflow for learning, running, debugging, and adapting the project. |
| [references/lab.json](references/lab.json) | Task IDs, source paths, prerequisites, command argv and cwd, effects, and acceptance conditions. |
| [references/lab.schema.json](references/lab.schema.json) | JSON Schema for the task manifest. |
| [references/agent-contracts.md](references/agent-contracts.md) | Actual runtime interfaces, resource ownership, failures, and command interpretation. |
| Existing [references/](references/), [concepts/](concepts/), and [troubleshooting/](troubleshooting/) | Exact source maps, explanations, and diagnosis for the selected task. |
| [scripts/validate_lab.py](scripts/validate_lab.py) and [tests/test_validate_lab.py](tests/test_validate_lab.py) | Offline document/metadata validation and its regression tests. |
| [references/agent-evals.md](references/agent-evals.md) | Representative agent requests and observable review criteria. |
| [VALIDATION_REPORT.md](VALIDATION_REPORT.md) | Checks performed for this delivered revision and checks still requiring services or a client. |

`references/lab.json` is discovery metadata for the Python teaching commands; the document checker never executes it. The separate `insta.yaml` describes commands and lifecycle actions for the Go CLI. See [INSTA.md](INSTA.md) for that integration and its pinned-source release boundary.

## 2. Check the source package before using services

With your selected Python environment active, from the lab root, install the
small validation dependency set:

```bash
python -m pip install -r support/requirements-test.txt
```

**Expected:** PyYAML and jsonschema are available in that environment. This
installation can contact the configured package index; the following checks
operate on local files and require no workshop credentials or model downloads.
The full retrieval dependencies in `support/requirements.txt` are separate.

Run the document and metadata check:

```bash
python scripts/validate_lab.py
```

**Expected:** exit `0` and one JSON result with `ok: true`. A validation failure
returns exit `1`; a missing validation dependency returns exit `2`. Fix the
reported path, contract, or document issue before relying on that metadata.
This checks structure and discoverability, not live retrieval behavior.

Run the checker's regression tests:

```bash
python -m unittest discover -s tests -v
```

**Expected:** the tests pass. Their deliberately damaged fixtures must be
rejected by the checker. These are tests of the new agent-support layer; they
do not run the workshop against OpenSearch or an LLM.

For a new project, work in the destination the user requested. Preserve the
original workshop for reference. When a complete copy is desired, a filesystem
copy must retain hidden files and the relative `common` links. For a project
inside an existing Git repository, follow that repository's instructions and
review the diff instead of introducing an unrelated repository or overwriting
an existing destination.

## 3. Choose a source pattern

Read [Choosing a retrieval approach](concepts/CHOOSING_RETRIEVAL.md) and select
the task whose behavior fits the target queries. Teaching order is not an
instruction to include every architecture in a new application.

| Need in the target project | Source pattern | What must remain explicit |
| --- | --- | --- |
| Meaning-based retrieval over short documents | [Baseline Vector RAG](references/baseline-vector-rag.md) | Semantic similarity does not establish document identity or a hard constraint. |
| Exact names, identifiers, and keyword matching | [Baseline BM25 RAG](references/baseline-bm25-rag.md) | This implementation includes a local NER score boost; it is not an NER-free BM25 example. |
| A server-side blend of lexical and semantic signals | [Hybrid RAG (Native)](references/hybrid-rag-native.md) | Uses one index and an OpenSearch search pipeline; ingestion updates its index-derived pipeline, and queries preserve it. |
| Structured exclusions or requirements before semantic refinement | [Hybrid RAG (NER + Rerank)](references/hybrid-rag-ner-rerank.md) | Two indexes must agree on IDs and metadata. The second phase is kNN refinement, not a cross-encoder. |
| Priority retrieval, escalation, and an evidence/audit record | [Governance Tiering](references/governance-tiering.md) | Tiers are document labels. Audit writes and citation checks have the limits documented in the source. |
| Monitoring and policy checks around that audit trail | [Operationalize Production](references/operationalize-production.md) | Adds operational examples; its validation is read-only; retention installation is a separate explicit administration step. |

For example, a support-article project with exact error codes and paraphrased
symptoms can start from the native hybrid task. If the project also requires
strict product/version selection, evaluate the two-phase filtering pattern
against those requirements. Decide using retrieval acceptance cases, rather
than assuming one method is superior for every corpus.

## 4. Adapt a project from that pattern

### Establish the implementation brief

Use the request and available data to establish the destination, audience,
corpus format and ownership, representative questions, required output,
provider settings, resource ownership, and acceptable failure behavior. Ask
for a decision only when it prevents a correct implementation; local inspection,
data mapping, and a scoped change plan can proceed while services are absent.

Read the selected source map and the actual `ingest.py`, `query.py`, and
`validate.py`. When changing a shared behavior, use
[architecture.md's change-intent map](architecture.md#3-task-to-source-map)
to identify affected tasks.

### Define the corpus contract before changing retrieval

The checked-in ingestors consume a JSON array of recipe objects. Their direct
field accesses require `recipe_id`, `recipe_name`, and `source`; `build_text()`
also reads ingredient and label lists. The ingestors do not expose a generic
CSV, PDF, directory, or arbitrary-document loader.

| Contract | Existing implementation to inspect | Adaptation decision |
| --- | --- | --- |
| Stable document identity | `main()` in the selected `ingest.py` | Define unique IDs and an update/delete policy. Re-ingestion upserts matching IDs but leaves records removed from the input corpus. |
| Searchable and embedded text | `build_text()` in the selected `ingest.py` | Choose which source fields become text and preserve relevant provenance. |
| Exact metadata and labels | [labels.py](support/common/labels.py), recipe mapping/query helpers in [opensearch_client.py](support/common/opensearch_client.py) | Define field names, normalization, allowed values, and missing-value behavior. |
| Embeddings and mapping dimensions | [embeddings.py](support/common/embeddings.py) and the selected index-creation helper | Align model, dimensions, and mapping. Existing indexes are not migrated automatically. |
| Retrieval and candidate identity | `ask()` in the selected `query.py` | Preserve intended filter scope and the relationship between grounding IDs and refined hits. |
| Context and generated answer | The selected `query.py` and [llm_client.py](support/common/llm_client.py) | Update domain language, context fields, and evidence rules together. |
| Verification | The selected `validate.py` | Replace recipe-specific fixtures and assertions with target-domain cases. |

For another recipe corpus, `--data-file` can point at compatible JSON. For a
different domain, make an explicit adapter or update the domain model in the
new project. Do not relabel unrelated fields as `cautions` or inherit the
recipe tiering rule without defining what those fields mean in the new domain.
There is no automatic natural-language-to-filter conversion: the existing
`--exclude` and `--require` arguments supply structured constraints explicitly.

### Keep code and resource changes scoped

Reuse the selected task and shared clients where their contracts fit. Preserve
the current source layout for a workshop extension. When a separate project
needs fewer components, keep the selected task's imports, shared dependencies,
and required NER service together, and remove unused copies and stale references
from that new project. Do not refactor the source workshop to match the copy.

Set distinct resource names for the new project. Read the manifest's resources
and [runtime contract](references/agent-contracts.md) before ingestion:

- Ingest index overrides affect that invocation. Queries still read their
  configured environment settings, so keep both destinations aligned.
- Two-phase retrieval and governance share the same BM25/vector index settings.
  Re-running the former's ingestor resets the governance tiers to `hot`.
- Native hybrid pipeline and retention-policy names are derived from their configured indexes. Use distinct index names for independent projects and review the ownership of existing resources before changing them.
- Changed mappings or embedding dimensions require an appropriate new index
  and reindexing plan. Current `ensure_*` helpers do not migrate existing data.

Prefer existing configuration and function arguments. Add a flag or setting
only when the requested behavior requires it. If changing governance, define
the tiering policy, escalation conditions, citation behavior, audit persistence
expectations, and retention behavior for the target domain rather than treating
the workshop's values as production defaults.

## 5. Preserve the human teaching structure

For a new workshop task, use an existing task as the example. Preserve this
top-level organization, replacing illustrative text with the actual topic:

```text
# Task N: Task title
Introduction explaining the task and its purpose
## What you will learn
The practical sequence is:
Step | Activity | Checkpoint table, one row per numbered step
## Understand the task topic
### Topic-specific explanations as needed
## Before you begin
## Step 1: enter the existing workspace
## Step 2: a concrete operation
Additional consecutively numbered steps as needed
## Completion checkpoint
```

Explain why an operation matters, link its implementing source beside the
explanation, identify the execution context, show the command, then give its
`Expected:` result before another exercise or section. Keep complete programs
in `scripts/`. Mark non-runnable excerpts as
`Implementation excerpt (not a standalone exercise)`.

Keep prerequisites and permissions under “Before you begin,” exact interface
details in the reference, diagnosis in troubleshooting, and overall acceptance
criteria in the completion checkpoint. Do not turn the learner page into a
metadata form or copy all reference material into the Skill.

For a new or changed task, align the README table, task number and ID, page
steps and anchors, source map, troubleshooting backlinks, and manifest entry.
When renaming a copied lab, update its manifest identity/title, matching schema
constants, Skill metadata, and links together.
The manifest's `requires` records task dependencies; command prerequisites
explain finer distinctions, such as needing the NER service from the BM25 task
without needing that task's populated index.

## 6. Verify the changed behavior

| Tier | Command or evidence | What it establishes |
| --- | --- | --- |
| Local source package | `python scripts/validate_lab.py` | Metadata/schema, path, documentation, Skill, and Python syntax checks. No runtime modules are imported or command records executed. |
| Checker regression | `python -m unittest discover -s tests -v` | The document checker detects the drift cases covered by its tests. |
| Workspace readiness | `python support/validate_setup.py` | Installed dependencies, configuration, and local readiness checks. Read its reference for optional service probes and credential-provider behavior. |
| Selected task behavior | Its direct `python -m src.validate` command or `make validate` | Live assertions for that task, with the service calls and writes recorded in the manifest. |
| Target-domain acceptance | Tests and observed evidence chosen for the new corpus | Whether the adapted project satisfies the requirements it actually claims. |
| Coding-tool behavior | [Agent evaluation cases](references/agent-evals.md) | Whether the chosen client finds and follows the workflow on representative requests. |

The vector baseline's validation deliberately reproduces a retrieval mistake
on the supplied recipes. Changing the corpus or embedding model can invalidate
that teaching assertion without proving the new project broken. Replace the
fixture and criterion deliberately; do not remove a failing assertion only to
obtain a green result.

For a new project, include known-answer retrieval cases, an ambiguous match,
hard-constraint cases when supported, no matching evidence, unavailable NER or
search, and any claimed citation/audit behavior. Assert document membership and
metadata constraints rather than an exact generated sentence. Investigate
warnings and verify audit persistence independently when it is a requirement.

Live checks require configured resources and may incur provider charges or
modify resources. Use the user's established authorization and the exact
target scope. Production validation can schedule deletion of the whole audit
index; do not run it as a routine document gate. If services are absent,
complete local work and report the precise integration checks left unrun.

## 7. Prompts for coding tools

### Portable entry prompt

```text
Read AGENTS.md and .agents/skills/instaclustr-rag-builder/SKILL.md in this
rag-retrieval folder. Use references/lab.json to select relevant tasks, then
read their source maps and implementing code. Use the existing human task
pages for teaching and USING_THE_TEMPLATE.md for adaptation.

My request: help me understand how the vector and BM25 tasks differ, then
identify which existing source files I would change for a new document corpus.
Do not connect to services for this explanation.
```

### Build a project from the template

```text
Use $instaclustr-rag-builder and USING_THE_TEMPLATE.md to build a separate
support-article RAG project from this workshop. Keep this source workshop intact.

Destination: a new support-rag directory beside this workshop.
Corpus: use the support-article JSON file I provide, with stable article IDs,
titles, bodies, product versions, and source URLs.
Retrieval goals: find exact error codes and paraphrased symptoms, and enforce
explicit product/version requirements when supplied.
Runtime: reuse the configured OpenSearch and OpenAI-compatible provider;
choose isolated project indexes and identify any cluster-wide resources.
Acceptance: include an exact-code case, a paraphrase, a mismatched version,
and no matching evidence. Preserve source provenance in the answer context.

Select and explain the source pattern, implement the scoped adaptation, and
update run instructions and checks. Use only the service access already
authorized for this project. Complete local checks before live checks and
report actual results. Do not implement a project-management CLI.
```

This example assumes the named corpus and destination match the real request.
Supply those inputs before asking for that build; the workshop does not include
a support-article dataset.

### Extend the workshop

```text
Use $instaclustr-rag-builder to add a retrieval task for the behavior I describe.
Read the closest existing task, its reference, and its source first. Preserve
the seven existing tasks and current paths. Add only the required code and
aligned teaching page, source map, troubleshooting, manifest, and checks.
Use explicit acceptance cases for the new behavior and report what was run.
```

## 8. Completion and publication

The completed change should be reviewable as code and documentation. Confirm
that the chosen pattern, corpus contract, resource names, run commands, error
behavior, and acceptance criteria agree. Run the local checks, execute the
authorized live checks appropriate to the change, and record their outcomes
separately. Keep unresolved service or client checks visible in the validation
record.

Review the diff for unnecessary moves, extra configuration, duplicated shared
code, stale links, and obsolete instructions. Archive the complete project with
relative symlinks and agent files preserved; exclude secrets, environments,
caches, generated logs, and repository administration files. A static pass is
evidence of a consistent source package, not a production-readiness claim.

## Design references and future scope

The supplied `deep-research-report.md` motivates task-addressable context,
repository instructions, focused Skills, machine contracts, and verifiable
execution. The supplied `USING_THE_TEMPLATE.md` motivates preserving the human
teaching composition. This implementation keeps the existing directory layout
and reuses source maps already present rather than duplicating them.

Current primary references for the agent layer:

- [OpenAI: instructions with AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md).
- [OpenAI: build Skills and local discovery](https://learn.chatgpt.com/docs/build-skills).
- [JSON Schema: Draft 2020-12](https://json-schema.org/draft/2020-12).

The Go `insta` project-management implementation discovers the labs from
[instaclustr/labs](https://github.com/instaclustr/labs). This package includes its
lab definitions; it does not contain the CLI binary or alter the CLI's pinned
catalog. Read [INSTA.md](INSTA.md) before replacing a generated project or
releasing this lab through a new CLI catalog revision. The Python interfaces,
source maps and teaching sequence remain usable without the CLI.
