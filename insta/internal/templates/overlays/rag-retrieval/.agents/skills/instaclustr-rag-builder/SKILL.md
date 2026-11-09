---
name: instaclustr-rag-builder
description: Build or adapt OpenSearch RAG projects from the Instaclustr rag-retrieval workshop. Use for learning, running, debugging, or extending this workshop and for new projects based on its vector, BM25, native hybrid, NER-filtered retrieval, or governance patterns. Keep requests for unrelated code outside this workflow.
---

# Build from the retrieval workshop

## Establish the project and intent

1. Locate the lab root containing `AGENTS.md`, `references/lab.json`, `scripts/`,
   and `support/`. Paths below are relative to that root. This Skill stays with
   the complete workshop; its links do not form a standalone plugin package.
2. Read [repository guidance](../../../AGENTS.md) and the
   [task manifest](../../../references/lab.json). Classify the request as learn,
   run, debug, extend an existing task, or build a separate project.
3. Select the smallest relevant task using the manifest and
   [retrieval choices](../../../concepts/CHOOSING_RETRIEVAL.md). Open its task
   page, existing source map, and actual source. Use the
   [architecture source map](../../../architecture.md#3-task-to-source-map)
   for changes to shared helpers. Do not load the entire workshop by default.

## Learn, run, or debug

- Follow the selected task's teaching sequence. Explain why the code makes each
  retrieval choice and what the expected evidence establishes.
- Before execution, read the command's `cwd`, `argv`, `prerequisites`, `effects`,
  and `result` in the manifest and the
  [runtime contract](../../../references/agent-contracts.md). Choose the
  configured local checkout and environment; the prebuilt container is a
  different source snapshot.
- Run the offline document checker first when available. Use the separate setup
  check for dependencies/configuration, then selected live commands only when
  their services and target resources are available and authorized.
- For a failure, follow the selected troubleshooting page and inspect the
  implementing exception/fallback path. Check exit status, stderr, retrieved
  evidence, and persistence where relevant. Do not turn an empty result or
  skipped check into a success claim.

## Build or extend

Follow [USING_THE_TEMPLATE.md](../../../USING_THE_TEMPLATE.md) for the concrete
adaptation sequence and completion criteria.

1. Identify the corpus, desired retrieval behavior, target project location,
   providers, resource ownership, hard constraints, and acceptance cases from
   the request and available files. Resolve only decisions that block a correct
   implementation; proceed with local inspection and reversible work meanwhile.
2. Select one existing retrieval pattern. Explain the choice using the target
   queries; the workshop sequence does not require every new project to use all
   five patterns.
3. Map source records to identifiers, text, provenance, and filter metadata.
   Inspect `build_text`, the active recipe mapping/query helpers, and the
   selected `ask` function. Decide whether to adapt recipe-shaped input or
   intentionally change the domain model in the new project.
4. Implement the scoped changes in the requested destination. Reuse the shared
   clients and configuration when compatible. Keep stable paths, isolate index
   names, preserve symlinks, and update mappings, retrieval, prompts, and tests
   together when fields change. Do not implement a generator or deployment CLI.
5. Define tests for retrieval membership, hard exclusions, unavailable evidence,
   and any claimed governance behavior. Replace recipe-specific teaching
   assertions when changing corpus/model; retain the property those assertions
   were meant to demonstrate.
6. Update the affected human task, reference, troubleshooting, and manifest.
   Run local checks and the authorized live checks relevant to the change.

## Report completion

Return the changed project, the chosen source pattern, how to run it, and
measured verification results. Name unrun integration checks and their missing
prerequisites. Do not claim Codex activation, container deployment, retrieval
accuracy, audit durability, or production readiness from static checks alone.
The [agent evaluation cases](../../../references/agent-evals.md) provide
representative requests and observable completion criteria.
