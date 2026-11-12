# Run RAG Retrieval with insta

`insta.yaml` is the executable lab contract for the Go CLI. The supplied task
pages, source maps, and `references/lab.json` remain the human and agent learning
material. Existing Python modules and shared `common` links remain intact.

## Managed path

Install Python 3.12 with `venv` and put the released `insta` binary on PATH.
Export `INSTACLUSTR_PROVISION_USERNAME` and `INSTACLUSTR_PROVISION_API_KEY` in
the invoking shell before requesting Instaclustr provisioning.

```bash
insta new my-rag --lab rag-retrieval
cd my-rag
insta config
insta up
insta exec
```

The CLI prepares its own Python environment, dependencies, and NER model without
manual activation or `.env` copying. `insta up` resumes an incomplete bootstrap,
ensures OpenSearch, and starts the declared NER service. It does not ingest the
recipe corpus or perform the educational queries and validators.

The shell takes precedence over saved configuration. Provisioning requires both
Instaclustr shell variables; they are separate from `OPENSEARCH_USER` and
`OPENSEARCH_PASSWORD` used by the application. To attach to an existing authorized
cluster, use `insta config set opensearch.mode attach` and configure those
connection values. Attached clusters are never deleted by `insta down`.

## Direct task operations

```bash
insta task list
insta task run baseline-vector-rag ingest
insta task run baseline-vector-rag query
insta task run baseline-vector-rag validate
insta task status baseline-vector-rag
insta down
```

Select the corresponding task ID for BM25, hybrid, governance, or production
operations. Commands run from the existing `scripts/<task>/` directories using
the managed interpreter and resolved environment. Read each operation's effects
before accepting changes. Production validation can install a whole-index
retention policy; a different index suffix does not isolate every shared
pipeline or policy.

## Verification and agent context

Use `insta exec` for a conversation, `insta exec -p "question"` for one prompt,
and `insta exec -i` for the native terminal. To preapprove declared task effects
in this lab session, run `insta exec -i --approve`. Codex receives the project
root, `AGENTS.md`, `insta.yaml`, and the selected task blueprint. No Skill activation is required. If Codex is unavailable,
direct lifecycle and task commands remain usable. Separately configured limited
Responses API assistance requires an explicit session choice.

A successful process exit is distinct from service readiness and task completion.
The vector baseline deliberately demonstrates a retrieval error; preserve that
teaching assertion. Inspect evidence, warnings, and the task's completion
checkpoint before claiming an exercise passed.

The supplied source includes `scripts/validate_lab.py` but omits the checker
regression tests mentioned in some source documents. Run the available checker
for structural evidence and report its results. Do not report missing tests as
executed. This integration adds no application behavior tests to the Python lab.
