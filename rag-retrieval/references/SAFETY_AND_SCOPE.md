# Safety & Scope

This project uses food-allergen/dietary questions as its running example
because they're concrete and factually checkable — "does this recipe
contain sulfites?" has a right answer, which makes retrieval failures and
fixes demonstrable rather than hand-wavy. That choice makes the *teaching*
concrete. It does **not** make this a dietary-safety product.

## This is not medical or dietary-safety advice

**Do not use this project, its answers, or its dataset to make real
allergen, dietary, or medical decisions.** Nothing here has been reviewed
for accuracy against any authoritative allergen database, and the LLM
generating answers can still be wrong even when it cites a real,
retrieved recipe correctly (see "What citation-tagging does and doesn't
guarantee" below). If you need real dietary-safety information, use a
verified source and consult a qualified professional.

## What each task's design is actually protecting

Each task narrows a specific, verified retrieval failure — not a general
safety guarantee:

- **Baseline Vector RAG → Baseline BM25 RAG**: that a retrieval system doesn't confuse two
  similarly-worded questions with different correct answers, by using
  exact keyword/entity matching instead of relying solely on semantic
  similarity.
- **Hybrid RAG (Native) → Hybrid RAG (NER + Rerank)**: that an exclusion like "no sulfites" is enforced as a
  boolean filter (`must_not`), which *cannot* be outvoted by a relevance
  score, rather than as a preference a ranking merely tends to honor.
- **Governance Tiering**: that data flagged as safety-relevant (any recipe with an
  allergen caution) is *always* reachable through the fast, default search
  path (`tier=hot`) rather than being contingent on it also happening to
  rank well — and that every query leaves a record of which tier(s) it
  actually searched and why, so "did this query reach the data it needed?"
  is answerable after the fact, not just at query time.
- **Operationalize Production**: that the governance signals Governance Tiering introduced (audit
  trail, citation validity, tiering thresholds) are actually watched,
  retained on a defined schedule, and can't drift without a recorded,
  approved change — not that the underlying answers are more accurate.

None of this makes the underlying **content** authoritative — it makes the
**retrieval and governance process** around whatever content exists more
reliable and auditable. Garbage in the source dataset is still garbage
out; these tasks don't validate the recipes' actual nutritional/allergen
accuracy against any ground truth.

## What citation-tagging does and doesn't guarantee

Governance Tiering's citation-tagging (`[R#]` handles, `hallucinated_citations`,
`citations_valid`) only checks that every citation the model outputs
points at something that was actually retrieved. It is a **grounding**
check, not a **correctness** check:

- It catches: the model inventing a citation tag for a recipe that was
  never in its context.
- It does **not** catch: the model correctly citing `[R1]` while
  misdescribing what `[R1]` actually says (e.g. claiming "no sulfites"
  while citing a recipe that does list sulfites). That would require
  claim-level fact verification against the cited text, which this
  project does not implement.

## Explicitly out of scope

- **Not a real dietary-safety product.** Built for teaching retrieval
  architecture, not for shipping to end users making real allergen
  decisions.
- **Small, curated, static dataset.** 260 recipes, chosen specifically
  *because* they contain a known ambiguous pair — not a representative or
  comprehensive corpus.
- **No claim-level fact-checking.** See above — citations are checked for
  grounding, not factual accuracy of the claim they support.
- **No response policy is enforced on governance failures.** Operationalize Production's
  `monitor.py` *detects* a hallucinated citation or an escalation-rate
  spike; it deliberately does not decide what to do about it (block the
  answer? retry? queue for human review?) — that's a product decision
  left to whoever operationalizes this further.
- **Single-tenant, single shared credential.** No user auth, no per-tenant
  isolation, and (before applying Operationalize Production's checklist) one shared
  OpenSearch credential across ingest/query/audit — see
  [`OPERATIONAL_CHECKLIST.md`](OPERATIONAL_CHECKLIST.md) section A.
- **No independent security review.** IAM policies and OpenSearch role
  definitions in `scripts/operationalize-production/src/hardening/` are
  illustrative examples to apply yourself, not audited, production-ready
  policies.
