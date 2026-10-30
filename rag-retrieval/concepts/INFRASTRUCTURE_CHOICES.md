# Infrastructure choices, and why

[Return to the task index](../README.md#tasks)

This page explains why the lab runs the pieces it runs, and where each one runs. The retrieval ideas themselves are on the other concept pages; this one is about the parts you have to install, configure or keep running.

## What runs where

| Piece | Runs | Needs | Used by |
| --- | --- | --- | --- |
| OpenSearch (Instaclustr) | Remote, hosted | Host and credentials in `.env` | Tasks 2 to 7 |
| LLM (Bedrock, or an OpenAI-compatible endpoint) | Remote | AWS access or an API key in `.env` | Generation in Tasks 2 to 6. Task 7's own commands make no LLM calls; its Step 2 runs a Task 6 query that does |
| Embedding model (`all-MiniLM-L6-v2`) | Local, on CPU, downloaded on first use | About 90 MB of disk and memory | Tasks 2, 4, 5 and 6 |
| NER service (spaCy, Flask) | Local, started by hand on `127.0.0.1:8000` | The `en_core_web_sm` model, about 13 MB | Tasks 3, 5 and 6; Task 7 only checks that it answers |
| Search pipeline `hybrid-search-pipeline` | On the cluster, one fixed name | Created by Task 4's ingest | Task 4 |
| ISM retention policy | On the cluster, one fixed name | The index-management plugin | Task 7 |

The two cluster objects with fixed names are shared by everyone who uses the same cluster. See [Architecture, section 6](../architecture.md#6-configuration-and-deployment-boundaries).

## Instaclustr-hosted OpenSearch

The lab uses a hosted cluster, not a local container. Every task's `create_client()` connects to the same cluster over HTTPS with basic auth from `.env`. Using one real cluster from Task 2 onward means the *retrieval architecture* is what is being taught, not "how do I run OpenSearch locally", and the same setup carries all the way to Task 7's production checklist without a rewrite.

## AWS Bedrock, or an OpenAI-compatible endpoint

The lab does not use a local model for generation. `generate()` in `support/common/llm_client.py` is the only function every task calls to get a completion. It dispatches to one of two isolated branches based on `LLM_PROVIDER` in `.env`:

- `bedrock` (the default): the Anthropic Messages API request and response shape, via `boto3`.
- `openai`: a plain `requests.post()` to any OpenAI-compatible `/chat/completions` endpoint, for anyone without Bedrock or AWS access. It adds no SDK dependency and is import-isolated from the Bedrock branch. Only the Bedrock path was exercised in this lab.

No other module depends on either provider's wire format, only on the signature of `generate()`, so no task's `ingest.py` or `query.py` changes with the provider.

## A small, local embedding model

The lab embeds with `sentence-transformers/all-MiniLM-L6-v2` (384 dimensions) on the CPU. It is small enough to run on a laptop with no GPU, which is the goal: every task should run on the machine you are already using. A larger model was not tested here, so this page makes no claim about how much it would change retrieval. The reasons for the choice, the limits it brings (such as the 256-token input) and the alternatives are on [Embedding models](EMBEDDING_MODELS.md).

## A local NER service

Entity extraction runs in a small Flask service (`ner_service.py`, spaCy `en_core_web_sm`) rather than in a managed NLP API. At about 13 MB and with no GPU, the model gains nothing from managed infrastructure, and a local process keeps the lab free of another account. The service exposes one route, `POST /ner`, and the tasks reach it through `NERClient`, which turns any failure into an empty entity list. That silent failure is the reason Task 7 monitors it. What the service extracts, what it misses and what you could use instead is on [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md).

## Tradeoffs and limits

- A hosted cluster needs an account, credentials and an IP allow-list entry, and it is shared. The lab is not self-contained the way a local container would be.
- A remote LLM means a network call and cost per question, and a local-only setup is not supported.
- The local pieces (embedding model, NER service) are started or downloaded by you, and nothing in the lab supervises them.
- None of the memory or latency figures here were measured under load. The sizes are those of the model files.

## Related pages

- [Embedding models](EMBEDDING_MODELS.md) and [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md): the two local models in detail.
- [Choosing a retrieval approach](CHOOSING_RETRIEVAL.md): which of these pieces each approach needs.
- [Architecture](../architecture.md): the shared `support/common/` package and index naming.
- [Task 1: Prepare the workspace](../tasks/setup-environment.md): configures these services.
- [Task 7: Operationalize Production](../tasks/operationalize-production.md): watching them in production.
