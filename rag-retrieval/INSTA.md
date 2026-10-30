# Using this replacement lab with insta

This archive preserves the seven-task lab layout and Python entry points. It includes
`insta.yaml` and the supplied `deploy-opensearch.json`. The deployment JSON is unchanged.
See [FIXES.md](FIXES.md) for the audit and changes and [VALIDATION_REPORT.md](VALIDATION_REPORT.md)
for executed checks and limits.

## Replace the source folder

Extract `rag-retrieval.zip` into a clean staging directory using an extractor that
preserves Unix symbolic links, such as `unzip` on Linux/macOS. The archive has one
top-level `rag-retrieval/` folder. Replace the repository's lab folder with it.
Do not merge the old copied `scripts/*/src/common` directories over the six restored
links. Each link must point to `../../../support/common`.

Keep your existing local credentials, `.env`, `.insta/`, virtual environment, and
learner edits separately before replacing an existing working project. These are
not distributed in this archive. Preserve the existing project state if updating
an already created insta project; the CLI will request approval for changed commands.

## Existing insta 0.1.0 bootstrap

The previously delivered Go CLI downloads an immutable GitHub revision and embeds
its own lab overlay. Replacing a folder in a local checkout does not automatically
change what that binary's `insta new` downloads. This ZIP is the corrected lab source,
not a rebuilt CLI release.

For a subsequent CLI release, commit the replacement lab to the repository, update
the pinned commit/archive in `insta/internal/bootstrap/catalog.go`, and align
`insta/internal/templates/templates.go`, its RAG overlay, and upstream file hashes
with that same revision. Run the CLI's source-preservation/bootstrap tests before
releasing the new binary. Do not bypass the revision/overlay integrity checks.

To use the corrected source now, replace the source files in an existing generated
project while preserving its `.insta/` state and local settings, or follow the native
Python instructions in [tasks/setup-environment.md](tasks/setup-environment.md).
There is no `insta init` or ZIP-import command in the 0.1.0 implementation.

## Configuration and execution

Current-shell values take precedence. Instaclustr provisioning requires
`INSTACLUSTR_PROVISION_USERNAME` and `INSTACLUSTR_PROVISION_API_KEY` in that shell;
they are never stored in this lab. Data-plane operations use `OPENSEARCH_HOST`,
`OPENSEARCH_PORT`, `OPENSEARCH_USER`, `OPENSEARCH_PASSWORD`, and `OPENSEARCH_SSL`.
Generation uses the selected provider's separate credentials.

In an initialized insta project:

```bash
insta up
insta task run setup-environment validate
insta task run baseline-vector-rag ingest
insta task run baseline-vector-rag query
insta task run baseline-vector-rag validate
```

Continue with each task's ingestion, sample query, and validation in the documented
sequence. `up` prepares the platform and NER service; it does not ingest or complete
teaching exercises. Query operations run fixed examples and exit. For a custom
question, use the existing Python `--question` and filter arguments from the task's
working directory with the project's Python environment and configuration.

Production checks use the existing audit records written by governance queries:

```bash
insta task run governance-tiering ingest
insta task run governance-tiering validate
insta task run operationalize-production policy-registry
insta task run operationalize-production monitor
insta task run operationalize-production validate
```

Production validation is read-only. It verifies real policy history, existing audit
records, NER health, and the status of optional retention. A retention SKIP does not
mean retention is applied. The explicitly documented retention administration step
can schedule whole-index deletion; it is not part of validation.

## Updating existing remote indexes

Native hybrid pipelines are now named `<configured-hybrid-index>-search-pipeline`.
Run that task's ingestion once to create its scoped pipeline before querying.
Existing shared pipelines are left for an administrator to review.

Retention policy IDs are now `<configured-audit-index>-retention`. A conflicting
existing policy is reported and is not overwritten. The lab does not migrate or
delete an existing cluster, policy, or index automatically.

Tasks 5 and 6 deliberately share their paired recipe indexes. After revisiting
Task 5 ingestion, rerun Task 6 ingestion before its governance checks. For a changed
corpus or embedding model, use fresh index names and reingest; ingestion upserts IDs
and does not remove records omitted from a replacement corpus. The default MiniLM
revision is pinned; different models require new retrieval expectations as well as
compatible dimensions.

## Optional Docker route

From `support/`, use Docker Compose 2.24 or newer:

```bash
docker compose up --build -d
docker compose exec lab bash
```

Compose builds this local source and bind-mounts the host lab folder. Changes persist
in that folder. The image's `/opt/venv` is already active; do not activate a host
`.venv` inside the container. Export the lab settings before starting Compose or use
an optional `support/.env`; no credential file is copied into the image. A native
lab-root `.env` is not the Compose interpolation file. The mounted folder must be
writable by the image's `lab` user (UID 1000) when editing or writing local caches.
Container health reports liveness. Run the setup and task checks separately.
