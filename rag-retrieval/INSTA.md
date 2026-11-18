# Run RAG Retrieval with insta

[insta.yaml](insta.yaml) is the executable lab contract for the existing Go CLI.
The task pages and [references/lab.json](references/lab.json) describe the same
Python modules for people and coding tools. This lab adds no alternate runner.

## Use the updated lab source

For an existing `insta` project, overlay the contents of this archive's
`rag-retrieval/` folder onto that project's root. Preserve its `.insta/` state,
configuration, and virtual environment; those generated files are not supplied
in the archive. Review local edits before overwriting matching source files.

For a new managed project, the updated folder must first be present in the
repository that the installed CLI uses. The inspected CLI revision selects
`rag-retrieval/` from `davidvonthenen/labs-mirror`, branch `main`. Placing this ZIP
on disk or updating only `instaclustr/labs` does not change that source selection.
No CLI source change is needed when replacing the lab in its configured source.
An extracted workshop can also run directly, following the
[setup task](tasks/setup-environment.md); `insta` project commands require the
state created by `insta new`.

## Managed path

Install Python 3.12 with `venv` and put the existing `insta` binary on PATH.
For provisioning, export `INSTACLUSTR_PROVISION_USERNAME` and
`INSTACLUSTR_PROVISION_API_KEY` in the invoking shell. They are separate from
application credentials for the OpenSearch cluster.

```bash
insta lab list
insta new my-rag --lab rag-retrieval
cd my-rag
insta config
insta up
insta task list
insta exec
```

The CLI prepares its Python environment, installs the declared requirements and
NER model, and creates a configuration marker without credentials. `insta up`
ensures the selected OpenSearch platform and starts the declared NER service.
It does not ingest the recipe corpus or run the learning exercises.

The shell takes precedence over saved configuration. Select an LLM model your
account or compatible endpoint can use; the lab's example model identifier is
not a provider-availability check. To use an existing authorized cluster, select
`insta config set opensearch.mode attach` and configure its connection values.
`insta down` retains attached clusters and destroys only owned resources tracked
by this stack.

The lab's `LLM_PROVIDER` defaults to `openai` and accepts `anthropic` or
`bedrock` as alternatives. Configure `OPENAI_API_KEY`/`OPENAI_MODEL` for
OpenAI-compatible generation, `ANTHROPIC_API_KEY`/`ANTHROPIC_MODEL` for the
Anthropic API, or `BEDROCK_MODEL_ID`, `AWS_REGION`, and AWS credentials/profile
for Claude on Bedrock. These settings are forwarded to the Python task
processes. They select the lab's answer-generation adapter, not a new CLI
coding backend.

For managed Bedrock queries/validation, the CLI checks AWS credentials before
loading the lab configuration. If no named profile is intended, run
`unset AWS_PROFILE AWS_DEFAULT_PROFILE` before `insta task run`; an explicitly
empty shell value can fail that preflight. Preserve a real named profile when
using one. Direct Python/Compose lab execution normalizes blank profile values.

## Run the existing task operations

```bash
insta task run setup-environment validate
insta task run baseline-vector-rag ingest
insta task run baseline-vector-rag query
insta task run baseline-vector-rag validate
insta task status baseline-vector-rag
```

Repeat `ingest`, `query`, and `validate` for the selected retrieval task. The
manifest also exposes the existing production `monitor`, `policy-registry`, and
`validate` modules. The task pages establish prerequisites and completion
criteria; [references/agent-contracts.md](references/agent-contracts.md) records
resource effects and output limits.

The CLI may call its own summary model before and after a task. That separate
implementation remains OpenAI-based; selecting an Anthropic or Bedrock lab
provider does not add those providers to CLI narration. Use the existing
`--no-summary` task option or `insta config set summary.enabled false` to
disable narration, or configure its separate summary provider/key/model for
OpenAI. Python retrieval queries still invoke the selected lab provider;
ingestion itself does not generate an answer. The CLI's own conversation
provider support and coding-agent backends are also unchanged.

**Chunk migration:** ordinary ingest deletes and recreates a selected index
whose mapping lacks `chunk_id`. The existing Python `--recreate` option also
deletes an index that already has a chunk layout. Confirm the intended indexes
before migration. Existing chunk indexes receive upserts, so removed recipes or
surplus old chunks remain until an explicit rebuild. No new migration flag was
added by this integration.

The vector baseline deliberately demonstrates a source-disambiguation error;
its validator also checks chunk token limits and grouping. Preserve those
teaching assertions. Index document counts count chunks, while query results
and `top-k` count grouped recipes. Native hybrid queries write the shared
`hybrid-search-pipeline` with current default weights `0.5/0.5`. Tasks 5 and 6
share paired indexes, and production validation can attach whole-index retention.

## Coding sessions and configuration

`insta exec` starts the CLI's conversation loop, `insta exec -p "question"`
submits one prompt, and `insta exec -i` opens the native Codex terminal. The
existing `--task` option supplies a particular task blueprint. Read
[AGENTS.md](AGENTS.md) and [USING_THE_TEMPLATE.md](USING_THE_TEMPLATE.md) for the
workshop and adaptation workflows; no Skill activation is required.

The CLI supplies the selected stack's application settings and managed Python
environment to its task processes and coding session. A coding client's shell
environment policy can further filter child-process variables. Prefer
`insta task run` for declared operations; for a custom question, use the
existing `python -m src.query --question ...` interface from that task directory
in the same configured environment. Never print credentials to diagnose missing
settings. A separately launched `codex`, Claude Code, or Cursor session does not
automatically receive a managed stack's saved configuration.

Codex is the implemented CLI backend in the inspected revision; the Claude Code
backend is a stub. Claude Code and other editors can still use this repository's
instruction files directly with the manual workshop runtime. Direct lifecycle
and task commands remain available without Codex.

After finishing the managed session:

```bash
insta down
```

