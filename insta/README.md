# insta

`insta` is a Go CLI for learning from, running, and adapting the Instaclustr
`rag-retrieval` and `agentic-rag` labs. Go owns the project, configuration,
provisioning, task execution, and cleanup. Use `insta exec` to discuss the
current lab with Codex, `insta exec -p` to ask one question, or `insta exec -i`
to open the native Codex terminal.

This update is source-only. Build the CLI for macOS or Linux with Go 1.24 or
newer; no prebuilt binaries are included. Windows remains outside the supported
platforms.

## Build and install this update

From this `insta/` directory:

```bash
go test ./...
go vet ./...
mkdir -p bin "$HOME/.local/bin"
go build -buildvcs=false -trimpath -o bin/insta ./cmd/insta
install -m 0755 bin/insta "$HOME/.local/bin/insta"
export PATH="$HOME/.local/bin:$PATH"
insta version
```

The version is `0.0.1`. In an existing lab, return to the project directory and
run `insta exec` with the rebuilt executable. Existing projects can use the new
commands immediately. See [FIREWALL_UPDATE.md](FIREWALL_UPDATE.md) for the
0.1.2 provisioning and recovery instructions.

Install Python **3.12** with its `venv` module. `insta new` creates the virtual
environment and installs the lab's requirements; there is no manual activation
or project-level pip command. Initial setup needs access to GitHub, the Python
package index, and the NER model download. Embedding models are cached by the
existing lab when first needed. The Python dependencies and models remain
external to the Go binary.

For the guided experience, install Codex and run `codex login`. The wrapped modes
also support authentication supplied directly to `codex exec`, such as
`CODEX_API_KEY`, or an existing profile for a local provider. See the
[Codex CLI guide](https://developers.openai.com/codex/cli/).

```bash
insta lab list
insta new my-rag --lab rag-retrieval
cd my-rag
```

`new` resolves the configured GitHub tag or branch (default `main`), downloads
that commit snapshot, adds the integration files, creates a `dev` stack, prepares Python and the NER model, and reports
Codex readiness. It does not create a cluster, ingest data, or open Codex.

### Configure and provision

Provide the management credentials through your existing shell environment:

```bash
export INSTACLUSTR_PROVISION_USERNAME='your-instaclustr-username'
export INSTACLUSTR_PROVISION_API_KEY='your-provisioning-api-key'
export OPENAI_API_KEY='your-application-openai-key'

insta config
insta up
insta exec
```

Use your normal shell credential management in place of the illustrative values
above. Provisioning stops if either Instaclustr variable is missing. They cannot
be stored with `insta config set`, and they are not forwarded to Codex or Python
tasks. The application OpenAI key, Codex login, and optional assistant key are
separate credentials.

`config` asks about provisioning or attaching and prompts for relevant missing
settings. Existing shell values take precedence. For RAG Retrieval, the defaults
follow the supplied example: OpenAI provider, `https://api.openai.com/v1`,
`gpt-6-luna`, TLS, and port 9200 unless the provider reports a different port.
Use `insta config set OPENAI_MODEL <model>` if a different model is appropriate
for your account. `OPENAI_BASE_URL` supports the existing RAG lab's compatible
Chat Completions endpoints; the separate limited assistant uses OpenAI Responses.

`up` displays the billable deployment definition and asks for confirmation.
It preserves the supplied OpenSearch version, node sizes, plugins, region,
network, and tier; the cluster name is made unique to the project and stack.
It records the accepted cluster ID, then waits for provisioning to complete:
the cluster must be `RUNNING`, any reported cluster operation must be
`NO_OPERATION`, and reported data centres and nodes must pass the provider's
running-state checks. Only then does it list the cluster's current firewall rules
and submit missing `OPENSEARCH` and `OPENSEARCH_DASHBOARDS` rules with network
`0.0.0.0/0`. The confirmation explicitly includes this exposure.
For an existing owned cluster, adding missing rules requires confirmation but no
new cluster create. Existing matching rules and duplicate HTTP 409 responses do
not cause failure. A failed request retains ownership for the next `up` or `down`.

**Security:** `0.0.0.0/0` allows all IPv4 source addresses. Use this requested
configuration only for disposable labs. TLS verification and application
authentication stay enabled; they do not replace a restrictive firewall. No
firewall changes are made in attach mode.

After both rules are present or accepted, `up` polls the authenticated OpenSearch
health endpoint until it reports green or yellow without a timeout. Provisioning
and application accessibility are separate waits, each bounded to 45 minutes and
supporting cancellation or an earlier caller deadline. Checks are separated
by 10 seconds, extended by bounded server `Retry-After` responses when present.
Network/startup errors, red health, and HTTP 408/429/5xx are retried; authentication,
certificate verification, configuration, and malformed health responses fail
without a polling loop. Application credentials returned by the provider are
stored using the existing protected store. When they remain absent after
provisioning, configure them through `insta config`.

OpenSearch Dashboards HTTP readiness is not probed. Its reported node status is
part of the management readiness check before firewall configuration. The stack
is marked `ready` only after the later authenticated OpenSearch health check.
Ingestion and query operations require a ready platform and recheck its current
health before execution. Interrupted waits retain the recorded cluster ID;
rerunning `up` resumes that cluster. `down` continues to delete only the recorded
owned cluster after confirmation; attached clusters remain untouched.

RAG `up` starts the local NER service. It does **not** run recipe ingestion,
queries, educational assertions, or retention-policy validation.

### Run exercises

```bash
insta task list
insta task start baseline-vector-rag
insta task run baseline-vector-rag ingest
insta task run baseline-vector-rag query
insta task run baseline-vector-rag validate

insta task run operationalize-production policy-registry
insta task status baseline-vector-rag
insta task stop baseline-vector-rag
insta down
```

The bundled RAG queries run the Python programs' built-in sample questions and
exit. The source also accepts `--question` for a custom question. `validate` retains the task's
existing assertions, including the vector baseline's intentional retrieval
failure demonstration.

The runner checks actual index document counts before declared query/validation
operations; an old successful ingestion record does not establish current data
availability. It records OS exit status, sanitized output, and the declared
verification meaning separately. Read the task's completion checkpoint and
warnings; closing an interactive program is not a completed exercise.

### Choose an exec mode

Run these commands from the lab project or one of its subdirectories.

| Command | What happens |
| --- | --- |
| `insta exec` | Opens a line-oriented conversation. Enter a question and press Return. Follow-ups resume the same Codex thread. Enter `/quit` or `/exit` on its own line to leave. End-of-file also closes the loop. |
| `insta exec -p "Explain the baseline retrieval failure"` | Runs one noninteractive prompt in an ephemeral Codex session, prints the response to stdout, and exits. Suitable for a script or output redirection. |
| `insta exec -i` | Opens the native Codex terminal. Use this mode for its full-screen interface, slash commands, and interactive approvals. An optional positional instruction can start the session. |

For a conversation focused on a particular task, for example, run

```console
$ insta exec --task baseline-vector-rag
```

Enter `Which index does this task populate?`, then `Which Python module reads that index?`.
In the loop, each submitted line is a separate turn. A new `insta exec` starts a
fresh conversation. To capture one answer in a file, run

```bash
insta exec --task baseline-vector-rag -p "Explain what the ingest operation writes." > answer.md
```

Both wrapped modes use the installed Codex CLI. The JSON event stream reports
completed assistant messages. The wrapper prints each as it arrives and shows
a thinking status while waiting when stderr is attached to a terminal. Use `--profile NAME` to select an
existing named Codex configuration. Without it, Codex uses its normal settings.
A conversation needs no named profile. The wrapper remembers the loop's thread
ID in memory and relies on Codex's normal session store for history. Removing
the lab directory does not erase Codex-managed history. One-shot prompts use
Codex's `--ephemeral` flag.

All modes start in the lab root with its resolved environment and task context.
The parent holds no project lock during a session, so Codex can invoke nested
lab commands. By default, Codex applies its configured sandbox and approval policy.

### Preapprove a session's lab tasks

```bash
insta exec -i --approve --task baseline-vector-rag "Execute the query operation."
```

When Codex calls `insta task run baseline-vector-rag query`, the runner lists
the effects and proceeds under the approval you supplied at launch. A successful
query still requires valid credentials, a ready OpenSearch cluster and a
populated index. The flag also works in the conversation loop and for one prompt:

```bash
insta exec --approve
insta exec --approve --task baseline-vector-rag -p "Execute the query operation."
```

`--approve` sets Codex's shell approval policy to `never`, selects
`workspace-write`, and enables network access for commands in the session.
Shell commands can write to the lab and contact external services. Declared task
operations may incur model charges or change remote data. The nested CLI matches
the lab root directory, project identity, selected stack and executable manifest
before accepting the task's effects. `--task` supplies teaching context. The
approval covers declared tasks in that project and stack.

Use a regular `insta exec` invocation to get the normal confirmation prompts.
Cluster lifecycle confirmations, review of changed manifests and the separate
limited assistant have their own approval paths. Commands outside the sandbox
or subject to an organization policy can still be denied. A task that declares
`mode: interactive` needs a terminal even when its effects are preapproved.
The bundled RAG sample queries declare `oneshot` mode. When using a Codex
profile with a custom `shell_environment_policy.include_only` allowlist,
include `INSTA_EXEC_TASK_APPROVAL` so the nested CLI can see the session's grant.

## Command surface

| Command | Behavior |
| --- | --- |
| `lab list`, `lab select <lab>` | List templates or choose the default for future projects. |
| `new <project> --lab <lab>` | Create a new project without overwriting a destination. |
| `stack list`, `stack select <stack>` | Select an existing environment in the current project; `dev` is created initially. |
| `config` | Prompt for relevant configuration. |
| `config list`, `get`, `set`, `remove` | Manage declared settings and show effective sources; secrets stay redacted. |
| `up`, `down` | Prepare prerequisites or clean up owned resources. |
| `task list`, `start`, `run`, `status`, `stop` | Select exercises and execute their declared operations. |
| `exec`, `exec -p <message>`, `exec -i [instruction]` | Start a conversation, submit one prompt, or open the native Codex terminal. All modes accept `--task`, `--profile` and `--approve`. |
| `version` | Show the build version. |

Global `--json` writes one command envelope to stdout for commands that support it.
All three `exec` modes, interactive `config`, and interactive task programs require
text mode. `--yes` confirms a displayed lifecycle/task action for
automation; it does not opt into direct API assistance.

Commands find the current project by walking upward from the current directory.
They never act on a remembered unrelated workspace. Lab, stack, and active task
are separate fields.

## Configuration and secrets

Precedence is: **current shell → stored stack value → observed provider output →
manifest default**. Provider-returned passwords are stored separately from
explicit user secrets. Empty optional settings are not injected as synthetic
environment variables, preserving source defaults and the AWS credential chain.

```bash
insta config list
insta config get OPENSEARCH_HOST
insta config set OPENAI_MODEL gpt-6-luna
insta config set OPENAI_API_KEY
insta config remove OPENAI_MODEL
```

Secret settings use a hidden prompt; passing a secret value in a command argument
is rejected. For unattended use, export its declared environment variable.
Unknown configuration keys are rejected. Manifest declarations can be extended
deliberately when adapting the lab; changed executable manifests require approval.

Nonsecret project state lives in `.insta/project.json`. Secrets use a private
file under the operating system's user configuration directory, outside the
project: `insta/secrets/<project-id>/<stack>.json`. Files use mode 0600 and
private directories. These permissions provide local access control, not
encryption. The CLI adds ignore rules and an empty `.env` compatibility marker
for the existing lab readiness checker; it never writes resolved credentials
into `.env` or overwrites an existing `.env`.

To use an existing cluster:

```bash
insta config set opensearch.mode attach
insta config set OPENSEARCH_HOST your-cluster-host
insta config set OPENSEARCH_USER your-opensearch-user
insta config set OPENSEARCH_PASSWORD
insta up
```

For Agentic RAG the actual password variable is `OPENSEARCH_PASS`. The manifests
follow the application source for each lab. Attached clusters and their indexes,
pipelines, and policies are never deleted by `down`. Operations you explicitly
run can still alter shared pipelines or retention policies; their effects are
displayed before execution. Index suffixes do not isolate cluster-wide policies.

For RAG's Bedrock option, configure `LLM_PROVIDER=bedrock`, `BEDROCK_MODEL_ID`,
and `AWS_REGION`. The standard AWS credential chain is preserved, including
profiles, SSO, workload roles, and environment credentials; static keys are not
mandatory. Generation operations resolve that chain before running. Ingestion
does not require an application LLM credential.

## Missing Codex and limited assistance

Missing/unconfigured Codex does not fail project creation or deterministic
commands. A deliberate `agent.backend=claude-code` selection returns a typed
not-implemented result; it never silently switches providers.

Optional assistance uses separate settings:

```bash
insta config set assistant.model your-responses-model
insta config set assistant.api_key
insta exec -i
```

The corresponding shell variables are `INSTA_ASSISTANT_MODEL` and
`INSTA_ASSISTANT_API_KEY`. If Codex is unavailable, `exec -i` asks for explicit
consent before sending a question or selected source material to OpenAI. This
assistant can list tasks, read bounded approved text, inspect status, and request
declared noninteractive task operations. It cannot edit files, run arbitrary
shell commands, provision resources, or delete clusters. Its tool loop is bounded,
and ordinary task confirmations still apply. Source reads exclude `.env`, state,
credentials, generated files, and datasets.

Instructions encourage native Codex to use `insta`; they are not an operating
system security boundary. Codex retains whatever permissions its own environment
grants. Management credentials are withheld, so provisioning should be run from
the invoking shell outside the coding session.

## Agentic RAG

```bash
insta new my-agentic --lab agentic-rag
cd my-agentic
insta config
insta up
insta task list
```

Its five teaching tasks are `introduction`, `news-agent`, `financials-agent`,
`orchestrator-agent`, and `client`. `up` prepares Python and OpenSearch.
`task start` starts the selected agent and its declared MCP/agent dependencies.
News queries require `TAVILY_API_KEY`; financial queries require `FINNHUB_API_KEY`.
The default path uses the existing external OpenAI implementation. The upstream
source hardcodes OpenAI's base URL on that path; this release does not claim an
Agentic `OPENAI_BASE_URL` override.

The original supported source did not include the historical news or financial corpus.
Supply these before their respective ingest commands:

- `scripts/news-agent/ai_media_dataset_20250911-1000lines.csv`
- Text-converted financial filings under `scripts/financials-agent/quarterly_filings/`

Leaving `OPENSEARCH_INDEX` unset preserves the distinct news and financial index
defaults. If you explicitly override it, that shell setting affects both
components. Agent/MCP probes establish transport availability; the orchestrator's
existing POST-only route is probed with GET expecting HTTP 405. None of those
probes claims successful paid inference or complete historical data.

## Recovery and ownership

A pending journal precedes every billable create. An accepted ID is saved before
polling and credential persistence. A timeout or malformed acceptance is treated
as uncertain; a repeated `up` searches for the recorded unique name instead of
repeating POST. A unique matching candidate requires explicit ownership approval.
If the result remains uncertain, inspect Instaclustr and retry reconciliation;
do not erase the journal to force another create.

Repeated `up` reconciles existing IDs. Runtime installation failures retain the
project and provide `insta up` as the retry path. Owned cluster deletion waits
for observed HTTP 404; failures retain their IDs for another `down`. Local
services can still be stopped if remote-management credentials are unavailable.

Service ownership is established by a private supervisor control socket and
instance token. A stale PID or occupied port is not treated as ownership.
`task stop` preserves shared/stack support services; `down` stops dependent
services first and cleans up owned remote resources. Source edits, configuration,
and unrelated cache entries are retained.

## Repository integration and development

Copy the ZIP's `insta/` folder into the `instaclustr/labs` repository root. Copy
the included `.github/workflows/insta-release.yml` into the repository's existing
workflow directory. Existing lab folders do not move.

```bash
cd insta
go test ./...
go vet ./...
go build -buildvcs=false -trimpath -o bin/insta ./cmd/insta
```

Go 1.24 or newer is required for source builds. This source update does not
contain prebuilt binaries. See `VALIDATION.md` for the checks executed here.

`internal/bootstrap/catalog.go` centralizes repository selection. `Tag` takes
precedence over `Branch`; an empty branch defaults to `main`. New project creation
resolves the selected ref to a commit and records the commit and archive digest.
Existing projects are not automatically refreshed. The prior branch/tag cache and
overlay behavior is preserved unchanged in this update.

`internal/templates` adds the bundled integration files without replacing
application Python. Changed upstream files are preserved; known baseline content
may receive the bundled overlay. Every project retains source provenance.

The downloadable RAG `insta.yaml` is also embedded at
`internal/templates/overlays/rag-retrieval/insta.yaml`. Keep its
`deploy-opensearch.json` beside it at the lab root. The schema is
`internal/templates/insta.schema.json`. Normal binary users receive all three
automatically through `new`.

The existing RAG Skill is preserved as source material; no Skill activation is
required. Kafka, Cassandra, ClickHouse, and Claude Code are explicit stubs. No
MCP server, plugin installer, general shell agent, or Pulumi state engine is added.

### Release

```bash
bash scripts/release.sh v0.0.1
```

This builds four archives and checksums under `dist/`. The included workflow runs
checks and publishes those assets when a maintainer pushes an `insta-v*` tag.
It has not been run against the remote repository as part of this delivery.

### Exit and result conventions

Structured errors include a stable category and, for task subprocess failures,
the separate child exit code. CLI codes are: 0 success, 1 general operation
failure, 2 invalid configuration/input, 3 unmet prerequisite/incomplete bootstrap,
4 declined action, 5 download failure, 6 task child failure, and 7 project busy.
Native Codex and one-shot prompts propagate the child's exit status. Operation records distinguish
running, exited, and failed from the task's verification statement.

See [VALIDATION.md](VALIDATION.md) for executed tests and unrun live integrations.
See [the provider contract](internal/platform/README.md) for the exact API paths,
authentication, response fields, readiness, and recovery rules.

## Official interface references

- [Instaclustr OpenSearch Provisioning V2](https://instaclustr.redoc.ly/Current/tag/OpenSearch-Provisioning-V2/)
- [Instaclustr firewall rules V2](https://instaclustr.redoc.ly/Current/tag/Cluster-Network-Firewall-Rule-V2/)
- [Instaclustr account cluster search](https://instaclustr.redoc.ly/Current/tag/Account-Cluster-List-V2/)
- [Connecting to an OpenSearch cluster](https://www.instaclustr.com/support/documentation/opensearch/using-opensearch/connect-to-an-opensearch-cluster/)
- [Codex CLI reference](https://developers.openai.com/codex/cli/reference/)
- [Codex non-interactive mode](https://developers.openai.com/codex/noninteractive/)
- [Codex configuration profiles](https://developers.openai.com/codex/config-advanced/#profiles)
- [Codex approvals, sandbox settings and network access](https://learn.chatgpt.com/docs/agent-approvals-security)
- [Codex authentication](https://developers.openai.com/codex/auth)
- [OpenAI Responses function calling](https://developers.openai.com/api/docs/guides/function-calling)

Licensed under the repository's Apache 2.0 license; see [LICENSE](LICENSE).
