# Run Agentic RAG with insta

This additive integration targets the pinned `instaclustr/labs` revision
`c321a65c9586706d8696645327e28b07f6b78b77`. Existing Python code and task paths
remain intact. `insta.yaml` is the executable contract; the existing task pages
remain the teaching material.

## Prepare the environment

Install Python 3.12 with `venv` and put the released `insta` binary on PATH.
Export `INSTACLUSTR_PROVISION_USERNAME` and `INSTACLUSTR_PROVISION_API_KEY` in
the invoking shell before creating an Instaclustr cluster. These provisioning
credentials are separate from OpenSearch's data-plane password and from model
or search-provider credentials.

```bash
insta new my-agentic --lab agentic-rag
cd my-agentic
insta config
insta up
insta exec
```

Use `insta exec -p "question"` for one answer, `insta exec` for follow-ups until
`/quit`, and `insta exec -i` for the native Codex terminal.

The Go CLI creates and uses its own Python environment. No manual activation,
repository clone, or `.env` setup is required. If runtime preparation is
incomplete, `insta up` resumes it. `insta up` prepares infrastructure; ingestion
and interactive demonstrations remain separate task operations.

To use an existing authorized cluster, set `insta config set opensearch.mode
attach` and configure its host, user, and `OPENSEARCH_PASS`. `insta down` never
deletes an attached cluster. The current shell takes precedence over saved
configuration. `insta config list` shows value sources with secrets redacted.

## Supply the historical corpora

The pinned source includes ingestion code but no news CSV or financial filing
corpus. Supply the following inputs before the corresponding ingest operation:

| Input | Project-relative location |
| --- | --- |
| Historical news CSV | `scripts/news-agent/ai_media_dataset_20250911-1000lines.csv` |
| Text-converted filing `.md` files and optional metadata | `scripts/financials-agent/quarterly_filings/` |

A preloaded cluster can provide the historical indexes instead. Query prechecks
inspect actual index contents. News and financial indexes have distinct source
defaults; leave `OPENSEARCH_INDEX` unset to retain them. A shared override applies
to both agents and requires deliberate data-model alignment.

## Run the teaching tasks

```bash
insta task list
insta task run introduction validate
insta task run news-agent ingest
insta task start news-agent
insta task run news-agent query
insta task run financials-agent ingest
insta task start financials-agent
insta task run financials-agent query
insta task start client
insta task run client query
insta task run orchestrator-agent audit
insta task status
insta down
```

Run ingestion only after supplying its source data. News ingestion replaces that
dataset's previous indexed snapshot. The two query operations use bounded,
declared example questions. Use the interactive client or Codex for a broader
learning conversation. Starting the client task starts its orchestrator and
specialist dependencies; running its query opens the terminal client.

The managed manifest uses external OpenAI and the source's default model
`gpt-5.4`, with `OPENAI_MODEL` available as a shell or saved override. Set
`OPENAI_API_KEY`, `TAVILY_API_KEY`, and `FINNHUB_API_KEY` for the services that use
them. Ingestion needs its search credentials and local embedding dependencies,
not all later service credentials. The source's OpenAI mode fixes the provider
URL; `OPENAI_BASE_URL` does not change it.

## What each check establishes

The introduction validation checks Python syntax. News/financial `/health`
probes and MCP initialization establish service availability without paid search
or model requests. The orchestrator exposes only POST `/v1/chat/completions`, so
its probe expects GET 405 on that existing route. These checks do not establish
retrieval quality, data coverage, model access, or successful teaching outcomes.
Inspect the response evidence and nonempty audit records using the task pages.

The MCP probe uses the protocol's documented
[initialization request](https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle)
and [HTTP transport headers](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports).
No live third-party integration is implied by the manifest's presence.
