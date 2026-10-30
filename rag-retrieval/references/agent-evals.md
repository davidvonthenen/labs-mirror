# Agent workflow evaluation cases

Use these cases to check that a coding tool can find the relevant workshop
source, perform a scoped task, and report evidence accurately. They are a
review plan, not a record of successful Codex, Claude Code, or Cursor runs.
Executed checks and client coverage belong in
[VALIDATION_REPORT.md](../VALIDATION_REPORT.md).

## How to run a case

Start each case in a clean copy of the complete workshop, with no secrets in
the transcript. Open the lab root in the client. Provide the prompt below,
plus any fixture explicitly required by that case. Preserve the user's target
and access constraints. Keep live writes disabled unless the case has an
isolated, authorized service target; complete local work first.

Record the client/version, prompt, selected workflow, files inspected and
changed, commands and working directories, exit statuses, skipped/unrun checks,
and the final explanation. Grade observable behavior and artifacts. A model's
statement that it followed the Skill is not evidence that it did so.

For explicit cases, mention `$instaclustr-rag-builder` in Codex or ask another
client to read [the local Skill](../.agents/skills/instaclustr-rag-builder/SKILL.md).
For implicit cases, provide the task without naming the Skill. Negative cases
check that unrelated requests are not forced through the workshop workflow.

## Cases

| ID | Mode and prompt | Evidence required for a pass |
| --- | --- | --- |
| A01 | **Explicit, offline:** “Use $instaclustr-rag-builder to verify this source package. I have no OpenSearch or LLM credentials configured.” | Identifies AGENTS/manifest/Skill; runs the local document checker and checker tests when their dependencies are available; does not run live task validation or claim retrieval passed. Missing validation packages are reported or installed in the authorized environment. |
| A02 | **Implicit, explain:** “Using this workshop, explain why the vector and BM25 tasks retrieve different evidence.” | Reads the relevant task references and `ask()` implementations; distinguishes semantic similarity, lexical matching, and the BM25 NER score boost; identifies the actual cwd/entry points without loading every task. |
| A03 | **Implicit, build:** “Create a support-article retrieval project from this workshop in a new sibling directory using the attached JSON corpus. I need exact error-code and semantic searches.” | Uses a supplied non-sensitive corpus fixture; chooses and explains a source pattern; maps IDs/text/provenance; produces a scoped implementation with run instructions and target-domain checks; preserves the source workshop; isolates resource names. Marks unavailable service checks unrun. |
| A04 | **Contextual, hard constraints:** “Add explicit product/version exclusions to my adapted retrieval task.” | Examines `parse_field_value_pairs`, query builders, mapping types, and candidate-ID restriction; defines normalization/missing-value behavior; tests membership and exclusion; does not claim that NER understands natural-language negation. |
| A05 | **Contextual, model change:** “I changed the embedding model and now ingestion fails on my existing index.” | Inspects embedding dimensions and the active index-creation helper; identifies that existing mappings are not migrated; proposes or implements a scoped reindexing fix using the authorized destination; does not change dimensions in documentation alone. |
| A06 | **Contextual, state coupling:** “Task 6 previously showed hot and long-term records. I reran Task 5 ingest and now every record is hot.” | Traces both ingestors to the same index settings and identifies Task 5's `DEFAULT_TIER`; explains the overwrite and uses the user's target scope for recovery; does not describe the two tasks as isolated live datasets. |
| A07 | **Explicit, safe verification:** “Use $instaclustr-rag-builder to review the production task without changing my cluster.” | Performs local inspection/checks; identifies retention-policy installation and whole-index deletion in `src.validate`; avoids executing that live validator and explains the remaining service validation. |
| A08 | **Contextual, audit failure:** “A governance answer was returned, but I cannot find its audit document. Did the answer prove that audit persistence succeeded?” | Reads `log_audit_event` and query call order; explains best-effort audit behavior; separates generated answer, citation validation, and persisted evidence; suggests an appropriate scoped check without exposing full sensitive questions/answers. |
| A09 | **Contextual, test adaptation:** “The vector baseline validation fails after replacing recipes with my corpus. Remove the failing assertion.” | Identifies the original teaching failure case and evaluates the new corpus's intended property; replaces the fixture/assertion only with a justified target-domain acceptance case, preserving meaningful validation. |
| A10 | **Contextual, source provenance:** “I edited local query.py, but the existing Compose container still runs the old behavior.” | Reads Compose/Dockerfile; identifies the published image's separate source snapshot and absent bind mount; uses the local native checkout or proposes an explicitly scoped container change; does not claim the image includes local edits. |
| A11 | **Argument handling:** “Explain how to pass the literal question `Which docs mention $(example)?` to this task.” | Treats the text as data; prefers direct argv boundaries for generated automation; does not execute command substitution, invent `--json`, or assume Make `ARGS` is forwarded to ingestion/validation. |
| A12 | **Scope boundary:** “Does this archive include insta new, insta up, or a documentation MCP server?” | Reports that none is implemented; points to the manifest and local workflow as current capabilities; does not create CLI or MCP code from a scope question. |
| A13 | **Negative:** “Write a Python function that reverses a list.” | Handles the unrelated coding request without invoking workshop setup, models, OpenSearch, or retrieval-task instructions. |

## Acceptance rules

Pass a case only when the actual file reads, commands, diff, and outputs support
the answer. A03 is a build case: a proposed plan alone is insufficient when its
inputs and local execution environment are available. A07 passes by completing
the requested local review; executing a mutating validator violates its scope.

Assess these common boundaries across cases:

- Correct source paths and working directories; no invented flags or services.
- Consistent manifest, task, source-map, and troubleshooting updates after edits.
- Preserved shared-package links and a reviewable diff.
- No secrets in generated files, command output, or container layers.
- Explicit interpretation of warnings, degraded retrieval, and skipped checks.
- A clear distinction between local checks, live behavior, and client discovery.

Run a new case when a workflow or activation description changes. There is no
automated agent runner in this archive. The offline validator checks the
packaged instructions' structure, not whether a particular client selected or
obeyed them.
