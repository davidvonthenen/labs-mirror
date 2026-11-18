# Task 6: Governance Tiering

Recipe-dataset task building on [Hybrid RAG (NER + Rerank)](hybrid-rag-ner-rerank.md).
Reuses Hybrid RAG (NER + Rerank)'s exact same `recipes-bm25` / `recipes-vector` indexes (see the
comment in `support/common/config.py`), but this time every recipe gets a real `tier`
value instead of Hybrid RAG (NER + Rerank)'s placeholder `"hot"` default, and every query is
recorded in a governance audit trail. Governance, not relevance alone, decides which recipes live in the hot tier and which in the long-term (LT) tier.

This task reuses the NER service from Baseline BM25 RAG ([Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md)). Each recipe's tier is copied onto every one of its chunks; [Chunking and governance](../concepts/CHUNKING.md#chunking-and-governance) explains why.

**Hot and LT, in one paragraph.** Each recipe gets a label, `tier`. **Hot** recipes are searched first, on every question. **LT** (long-term) recipes are searched only if the hot search isn't confident, which is called escalation. The label is stored on the recipe, and both tiers sit in the same index: nothing is archived or made cheaper. Here a recipe is `hot` if it lists any allergen caution and `lt` if it lists none, because allergen information must never be on the slow path.

**Not all data is equally safe to demote.** This is the mistake of tiering
data by cost alone without checking whether some of it is safety-critical
— plus the "why did it say that?" problem: this task adds the record
you'd need to answer that question after the fact.


**Teaching goal:** "Tiering" (splitting data into a fast/default tier and a
slower, lower-priority tier) is usually framed as a pure cost/performance
optimization. This task frames it as a **governance decision instead**:
which data is *not allowed* to be relegated to the slow/optional path, and
how do you prove — after the fact — that a query correctly reached the data
it needed to?

## What you will learn

By the end, you will have re-indexed the sample with a safety-driven tier, seen a hot-tier question resolve without escalation and an LT-tier question escalate, read the citation handles in each answer, and learned what audit record each query leaves behind. The policy is [`assign_tier()`](../scripts/governance-tiering/src/ingest.py); the escalation, citation checks, and audit write are in [`ask()`](../scripts/governance-tiering/src/query.py).

The practical sequence is:

| Step | Activity | Checkpoint |
| --- | --- | --- |
| [1](#step-1-enter-the-existing-workspace) | Re-enter the workspace and open the task directory. | `Makefile` and `src/` are listed. |
| [2](#step-2-start-the-ner-service) | Start Baseline BM25 RAG's NER service in Terminal 1. | Flask reports it is serving on `127.0.0.1:8000`. |
| [3](#step-3-ingest-with-governance-tiers) | Re-chunk and re-index with `assign_tier()`, and create the audit index. | The log reports `Ingested 260 recipes as 1823 chunks ... tier distribution (recipes): hot=209, lt=51`. |
| [4](#step-4-ask-hot-tier-lt-tier-and-filtered-questions) | Ask a hot-tier, an LT-tier, and a filtered question. | `escalated=False` for the first, `escalated=True` for the other two, and `valid=True` citations. |
| [5](#step-5-validate) | Run the smoke test with NER still running. | It prints four `[PASS] Governance Tiering ...` lines. |

## Understand governance-driven tiering

### The tiering policy

The policy is one function, [`assign_tier()` in `ingest.py`](../scripts/governance-tiering/src/ingest.py): a recipe with any allergen caution is `hot`, and a recipe with none is `lt`. This probe applies it to the sample and to the two Fish and Chips recipes. It needs no cluster, no NER service and no LLM, and writes nothing. Run it from `scripts/governance-tiering/` with the `.venv` active.

```bash
python - <<'PY'
import json
from collections import Counter
from src.common.config import RECIPES_SAMPLE_PATH
from src.ingest import assign_tier

recipes = json.loads(RECIPES_SAMPLE_PATH.read_text(encoding="utf-8"))
print("tier counts:", dict(Counter(assign_tier(r) for r in recipes)))
for r in recipes:
    if r["recipe_name"].lower() == "fish and chips":
        print(f"{r['recipe_name']:15s} {r['source']:26s} cautions={r['cautions']}  ->  {assign_tier(r)}")
print("empty cautions list ->", assign_tier({"cautions": []}), "| cautions key missing ->", assign_tier({}))
PY
```

**Expected:** the tier counts, the two Fish and Chips recipes, and the two edge cases.

```text
tier counts: {'hot': 209, 'lt': 51}
Fish and Chips  thegratefulgirlcooks.com   cautions=[]  ->  lt
Fish and chips  womensweeklyfood.com.au    cautions=['Sulfites']  ->  hot
empty cautions list -> lt | cautions key missing -> lt
```

The two edge cases matter: a recipe whose caution data is missing is treated exactly like one with no cautions, so it lands in `lt`. The policy is only as good as the field it reads.

A tier here is a label stored on each recipe (and copied onto every one of its chunks), not a move to different storage. Both tiers sit in the same index, and the `tier` field decides which search path reaches a recipe first. Nothing is archived, compressed or made cheaper by being `lt`.

Any recipe with an allergen caution (Sulfites, Wheat, Soy, ...) is `tier=hot`
— safety-relevant data must always be in the default, low-latency search
path. Recipes with **no** cautions are `tier=lt` — lower priority, since
getting one of those slightly slower or via a fallback path has no safety
consequence. Of the 260-recipe sample: **209 are `hot`, 51 are `lt`.**

This is the opposite of the usual "hot = recent/popular" heuristic — the
policy is driven by a compliance requirement, not by traffic patterns. That
inversion is the point: the *governance* rule decides the *tiering* rule,
not the other way around.

### Query flow: hot-first, escalate on low confidence

`query.py` builds directly on Hybrid RAG (NER + Rerank)'s ground (BM25) → refine (vector)
pipeline, adding a tiering decision before the grounding step:

1. Run the BM25 grounding query **restricted to `tier=hot`** (same
   `bm25_search()` helper as Hybrid RAG (NER + Rerank), now passing `tier="hot"`).
2. **Escalate** to a second grounding query across **all tiers** (no tier
   filter) if the hot-tier search wasn't confident. Three independent
   triggers, in order of how they're checked:
   - **No hot candidates at all.**
   - **A specific source is named in the question but isn't present among
     the hot-tier results** (`named_source_not_in_hot_tier`). This one
     matters in practice: a generic BM25 score can be high just from
     matching the recipe *name* (e.g. "Fish and Chips" appears in both the
     hot and lt copies), even when the *specific* source the user asked
     about was never actually found. Checking the aggregate top score alone
     isn't enough — the question is parsed for anything that looks like a
     domain (`thegratefulgirlcooks.com`) and that's checked directly
     against the hot-tier hits' `source` field.
   - **Low confidence otherwise**: fewer than `TIER_MIN_HOT_CANDIDATES`
     (default 3) hits, or a top BM25 score below `TIER_MIN_HOT_SCORE`
     (default 15.0) — tuned against this corpus's real scores, where an
     exact name/source match scores ~22–28 and a loose incidental
     word-overlap match scores ~3–9.
3. Vector-refine (same as Hybrid RAG (NER + Rerank)) over whichever candidate set resulted,
   restricted to those recipe IDs. One subtlety: kNN's
   `include_recipe_ids` filter is a *post*-filter over OpenSearch's top-N
   nearest neighbors, so the refine step asks for a neighbor pool as large as the
   index (`ann_pool_for_filtered_search()`, about 1,823 chunks) rather than a
   small one — otherwise a grounding candidate with weak *vector* similarity
   to the question (exactly the failure Baseline Vector RAG demonstrated) could be
   correctly found by BM25 grounding and then silently dropped again by an
   under-sized vector refine step. A fixed pool of 300 was enough when a recipe was one document; with chunks it keeps only 10 of 25 candidates. [Hybrid RAG (NER + Rerank)](hybrid-rag-ner-rerank.md#see-grounding-and-refinement-without-the-model) runs this on real data: pools of 25, 300 and 1,823 keep 4, 10 and all 25 of the 25 grounded recipes.
4. **Pin the named source.** The question "does the X.com recipe contain sulfites?" is mostly about sulfites, and the allergen chunk of nearly every recipe matches that, so the vector step can bury the recipe the user named even though BM25 ranked it at the top. If the question names a source (`_extract_domain_tokens()`) and no refined recipe comes from it, the best *grounded* recipe from that source is added to the evidence, using its own best-matching chunks (a second kNN query restricted to that one recipe). The pin is recorded in the audit record (`pinned_named_sources`) and printed as a `PINNED:` line. It never adds a recipe that failed grounding, so `--exclude` and `--require` still hold.
5. Generate the answer with the LLM.
6. **Write a governance audit record** to a new `recipes-audit-log` index
   (`ensure_audit_index()` / `log_audit_event()` in
   `support/common/opensearch_client.py`) capturing: the timestamp, the question, extracted
   entities, any `--exclude`/`--require` filters, which tier(s) were
   searched, whether escalation happened and why, any pinned source, candidate/result counts,
   the final recipe IDs/sources/cautions, the evidence handles and the chunk IDs returned as evidence,
   the handles the model cited, any cited handle that was not in the evidence, whether the citations
   were valid, the answer, and latency.
   This is the compliance trail: you can always answer "did this query reach
   the long-term tier, and why (not)?" after the fact by querying
   `recipes-audit-log` itself.

### See the escalation decision without the model

This probe repeats the escalation decision that `ask()` makes for four questions: the hot-tier BM25 search, then the three checks (too few hits or a score below the minimum, and a named source missing from the hot hits). It stops there. It does not run the second search, the vector step or the model, and it does not write an audit record. The decision expressions are copied from `ask()` and the probe calls the same helpers, so if you change the rule in `query.py` you must change the probe too. It needs the NER service from Step 2 and the indexes from Step 3, and reads only the BM25 index.

```bash
python - <<'PY'
from src.common.chunking import chunk_fetch, group_by_recipe
from src.common.config import load_settings
from src.common.ner_client import NERClient
from src.common.opensearch_client import bm25_search, create_client, normalize_hits, parse_field_value_pairs
from src.query import _extract_domain_tokens

s = load_settings()
client, ner = create_client(s), NERClient(s)
cases = [
    ("Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?", []),
    ("Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?", []),
    ("Suggest a British fish and chips recipe.", ["allergen=Sulfites"]),
    ("A dish with 2.5 cups of flour.", []),
]
for question, excl in cases:
    entities = ner.extract_entities(question)
    response = bm25_search(client, s.opensearch_recipe_bm25_index, question, k=chunk_fetch(25),
                           entities=entities, exclude=parse_field_value_pairs(excl), tier="hot")
    hot = group_by_recipe(normalize_hits(response), top_k=25)  # chunks in, recipes out
    top = hot[0]["score"] if hot else 0.0
    named = _extract_domain_tokens(question)
    sources = {(h.get("source") or "").lower() for h in hot}
    missing = bool(named) and not any(t in src for t in named for src in sources)
    too_few = len(hot) < s.tier_min_hot_candidates
    low_score = top < s.tier_min_hot_score
    escalate = not hot or too_few or low_score or missing
    reason = None
    if escalate:
        reason = "no_hot_candidates" if not hot else "named_source_not_in_hot_tier" if missing else "low_confidence_hot_score"
    print(question)
    print(f"  named_sources={named}  hot_hits={len(hot)}  score_below_min={low_score}  named_source_missing={missing}")
    print(f"  -> escalate={escalate}  reason={reason}")
PY
```

**Expected:** four questions, each with the three signals and the decision. The raw scores are not printed because they drift with the index; the decisions are the property to check.

```text
Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?
  named_sources=['womensweeklyfood.com.au']  hot_hits=25  score_below_min=False  named_source_missing=False
  -> escalate=False  reason=None
Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?
  named_sources=['thegratefulgirlcooks.com']  hot_hits=25  score_below_min=False  named_source_missing=True
  -> escalate=True  reason=named_source_not_in_hot_tier
Suggest a British fish and chips recipe.
  named_sources=[]  hot_hits=8  score_below_min=True  named_source_missing=False
  -> escalate=True  reason=low_confidence_hot_score
A dish with 2.5 cups of flour.
  named_sources=[]  hot_hits=25  score_below_min=True  named_source_missing=False
  -> escalate=True  reason=low_confidence_hot_score
```

The first three decisions match the three Step 4 commands. For the thegratefulgirlcooks.com question, the best hot-tier score is not low, because the other Fish and Chips recipe has the same name, so only the missing source triggers the escalation. The fourth question shows the other side of the source detection: "2.5" is not taken for a source (`named_sources=[]`), but the question escalates anyway because the best hot-tier score is below the minimum. Source detection prevents a *false source* escalation; it does not prevent a low-confidence one. Thresholds like 15.0 are tuned to this corpus's scores, and [Task 7](operationalize-production.md) adds a check that they have not been changed without a record.

Two details about the inputs. The source check uses a regular expression (`_extract_domain_tokens()`), not NER, and it compares the lowercased token with each hot-tier source as a substring, so a token such as `food.com` would also match a source named `myfood.com`. The entities from the [NER service](../concepts/NER_IN_RETRIEVAL.md) do feed the BM25 score that is compared with the minimum, so an NER outage could move a question across the threshold; that effect was not tested here.

### Output-level governance: citation-tagging

The tiering/escalation/audit-log trail above answers "did this query reach
the right *data*, and why?" It doesn't answer a related question: "which
retrieved chunk backs *this specific sentence* in the answer?" This task
adds that second, complementary governance signal on top of the first.

Every recipe handed to the LLM is tagged with a stable handle (`[R1]`,
`[R2]`, ...) in `_build_context()`; a handle names a *recipe*, and the chunks that were shown as its evidence are recorded in the audit record's `evidence_chunk_ids`, in the same order as `refined_hits` (and
therefore the same order as `refined_recipe_ids` in the audit record, so a
handle can always be mapped back to a recipe ID from the audit log alone).
`_build_system_prompt()` then makes citing those handles mandatory.

This probe prints the system prompt that [`_build_system_prompt()`](../scripts/governance-tiering/src/query.py) builds for two retrievals, one with two evidence handles and one with none, then runs the citation check on a made-up answer. It needs no cluster, no NER service and no LLM, and writes nothing. Run it from `scripts/governance-tiering/` with the `.venv` active.

```bash
python - <<'PY'
import textwrap
from src.query import _build_system_prompt, _extract_cited_handles

for handles in (["R1", "R2"], []):
    print(f"allowed_handles={handles}")
    print(textwrap.fill(_build_system_prompt(handles), width=100))
    print()

evidence = ["R1", "R2", "R3"]
answer = "No, it has no sulfites. [R2] It serves four. [R2][R7]"
cited = _extract_cited_handles(answer)
print("cited handles:", cited)
print("hallucinated :", [h for h in cited if h not in evidence])
PY
```

**Expected:** the two prompts (wrapped at 100 columns by the probe), then the citation check.

```text
allowed_handles=['R1', 'R2']
You are a fact-focused recipe assistant. If the answer is not grounded in the snippets, respond with
'I don't know.' Be precise about which recipe and source you are answering about -- do not blend
facts from different recipes together. Never recommend a recipe that the context marks as containing
an excluded allergen. CITATION RULES (mandatory): each recipe snippet in the context is wrapped in a
handle tag like [R1]. Allowed citation tags for this answer: [R1] [R2]. After every sentence
containing a factual recipe claim, append the [R#] tag(s) for the snippet(s) that support it. Use
opening tags only (e.g. [R1]) -- never closing tags like [/R1]. Never invent a tag that is not in
the allowed list above.

allowed_handles=[]
You are a fact-focused recipe assistant. If the answer is not grounded in the snippets, respond with
'I don't know.' Be precise about which recipe and source you are answering about -- do not blend
facts from different recipes together. Never recommend a recipe that the context marks as containing
an excluded allergen. No recipe evidence was retrieved for this question, so there are no citation
tags available -- say you don't know rather than guessing or inventing a citation.

cited handles: ['R2', 'R7']
hallucinated : ['R7']
```

The prompt changes with the evidence: the allowed tags are listed by name, and with no evidence the model is told there are none and to say it does not know. The prompt is an instruction, not a control. Whether a model obeys it is what the check below it measures. The answer in the probe is invented to show a hallucinated tag (`R7` was never retrieved); no model produced it.

After the LLM responds, `_extract_cited_handles()` parses every `[R#]` tag
out of the generated answer, and the handles it finds are checked against
the handles that were actually retrieved:

- `evidence_handles` — every handle that was made available to the LLM.
- `cited_handles` — every handle the LLM actually used, in first-seen order.
- `hallucinated_citations` — any cited handle **not** in `evidence_handles`
  (i.e. a citation the model invented rather than grounded).
- `citations_valid` — `True` iff `hallucinated_citations` is empty.

These checks detect and record; they do not enforce. `query.py` prints the
answer even when `citations_valid` is `False`; the only effect is the
`CITATIONS:` line and the audit record. Acting on an invalid citation is
left to [Task 7](operationalize-production.md), whose monitor alerts on it.

All four are written into `governance_meta` (printed by `query.py`) *and*
into the same `recipes-audit-log` record as the tiering fields, so a single
audit document captures both governance layers: which tier(s) were
searched and why, and whether the final answer's citations were honest.

### Architecture

![Governance Tiering architecture diagram](../images/governance-tiering/diagram.png)

### Dataset

Same curated 260-recipe sample as Baseline Vector RAG, Baseline BM25 RAG, Hybrid RAG (Native) and Hybrid RAG (NER + Rerank), in
`support/recipes/recipes_sample.json`, with the synthetic page text and chunks described in [Baseline Vector RAG](baseline-vector-rag.md#why-this-lab-chunks). The running "Fish and Chips" pair splits
across tiers by design:

| | Source | Cautions | Tier |
|---|---|---|---|
| Recipe A | thegratefulgirlcooks.com | *(none listed)* | **lt** |
| Recipe B | womensweeklyfood.com.au | Sulfites | **hot** |

Asking specifically about Recipe A should therefore require escalation
(it's excluded from a hot-only search), while Recipe B should resolve
without ever touching the LT tier.

## Before you begin

Complete [Task 1: Prepare the workspace](setup-environment.md). This task reuses Baseline BM25 RAG's NER service and re-indexes Hybrid RAG (NER + Rerank)'s `recipes-bm25` / `recipes-vector` indexes, overwriting their placeholder tiers; you do not need to have run Hybrid RAG (NER + Rerank) first.

Use two terminals with the `.venv` active: Terminal 1 in `scripts/baseline-bm25-rag/` for the NER service, Terminal 2 in `scripts/governance-tiering/`. Each `python -m src.<module>` command has a matching Make target, listed in the [Governance Tiering reference](../references/governance-tiering.md#commands). Every query in this task writes one document to `recipes-audit-log`, including the question and answer text.

## Step 1: enter the existing workspace

From the lab root, in Terminal 2:

```bash
source .venv/bin/activate
cd scripts/governance-tiering
ls
```

**Expected:** `Makefile` and `src`.

## Step 2: start the NER service

Terminal 1 (a new terminal opened in the lab root; NER service, stays running):

```bash
source .venv/bin/activate
cd scripts/governance-tiering
python -m src.ner_service
```

**Expected:** Flask's startup message, including `Running on http://127.0.0.1:8000`. Reuse it if it's already running from an earlier task.

## Step 3: ingest with governance tiers

Terminal 2:

```bash
python -m src.ingest
```

**Expected:** a log line `Ingested 260 recipes as 1823 chunks into 'recipes-bm25' and 'recipes-vector' -- tier distribution (recipes): hot=209, lt=51`. The counts are recipes: every chunk of a recipe gets its recipe's tier. If either index has the older one-document-per-recipe layout, ingest logs a `WARNING`, deletes it and creates it again for chunks. Add `--recreate` to rebuild indexes that already hold chunks. Hybrid RAG (NER + Rerank)'s ingest writes the same two indexes with a placeholder tier, so if you rerun that ingest later, rerun this one before querying again.

## Step 4: ask hot-tier, LT-tier, and filtered questions

```bash
# Hot-tier recipe -- should resolve without escalating
python -m src.query --question "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?"

# LT-tier recipe -- should escalate to find it
python -m src.query --question "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?"

# --exclude/--require from Hybrid RAG (NER + Rerank) still work, combined with tiering
python -m src.query --question "Suggest a British fish and chips recipe." --exclude allergen=Sulfites
```

**Expected:** each command prints a `GOVERNANCE:` line, a `CITATIONS:` line, the `ANSWER:`, and the `REFINED` recipes (with the sections of their matching chunks in brackets). The LT-tier question also prints a `PINNED:` line. Scores, such as `hot_top_score`, and the answer wording drift between runs; the properties to check are `escalated`, `reason`, `cited`, and `valid`. Sample output for each command follows.

**Hot-tier recipe** (womensweeklyfood.com.au, has a Sulfites caution): `escalated=False` and `valid=True`. It resolves without ever touching the LT tier, and the answer cites exactly the retrieved recipe that backs it (`[R1]`), with no hallucinated tags:

```text
GOVERNANCE: tiers_searched=['hot'] escalated=False reason=None hot_hits=25 hot_top_score=22.7312
CITATIONS: evidence=['R1', 'R2', 'R3', 'R4', 'R5'] cited=['R1'] hallucinated=[] valid=True
ANSWER: Yes, the womensweeklyfood.com.au Fish and Chips recipe contains sulfites. [R1] The
recipe is marked with a sulfites caution in its allergen information. [R1]

REFINED (vector rerank, top 5 recipes; matching chunks in brackets):
  - [R1] Fish and chips | source=womensweeklyfood.com.au | tier=hot | cautions=['Sulfites'] | score=0.8227 | [Ingredients, Allergen cautions and labels, Method steps 7-8]
  - [R2] Chocolate Stout Whoopie Pies | source=The Daily Meal | tier=hot | cautions=['Sulfites'] | score=0.7682 | [Ingredients, Allergen cautions and labels]
  ...
```

**LT-tier recipe** (thegratefulgirlcooks.com, no cautions): `escalated=True`, `reason=named_source_not_in_hot_tier`, `cited=['R6']`, and `valid=True`. The hot-only
grounding search's top hit is the *other* Fish and Chips recipe (score
28.73: the recipe *name* matches regardless of source, and the question's
`fish and chips` entity from NER matches the recipe name that ingest folded
into that recipe's entities, which adds 6.0). The question
names a source domain (found by `_extract_domain_tokens()`, which accepts only domain-like tokens such as `name.com` or `name.com.au`), and that source
isn't among the hot-tier results, so the system escalates. The correct
recipe in the LT tier scores the same 28.73, because the same entity matches
its name too. Neither the score nor the entity boost is why the system
escalated: the named source was missing from the hot tier.

The vector step then leaves that recipe outside its top 5, because the question is mostly "contain sulfites?", which matches the allergen chunk of nearly every recipe (the same effect as in [Baseline Vector RAG](baseline-vector-rag.md#see-the-miss-without-the-model)). The recipe the user named would have been missing from the evidence, so the named-source rule **pins** it: the `PINNED:` line reports that its best chunks were added after the vector refine. It is added last, which is why it is `[R6]` and the answer cites `[R6]` — the handle assigned to the *escalated*, LT-tier recipe — not `[R1]`, the hot-tier recipe that merely shares a name:

```text
GOVERNANCE: tiers_searched=['hot', 'lt'] escalated=True reason=named_source_not_in_hot_tier hot_hits=25 hot_top_score=28.7312
PINNED: named source(s) ['thegratefulgirlcooks.com'] added to the evidence after the vector refine
CITATIONS: evidence=['R1', 'R2', 'R3', 'R4', 'R5', 'R6'] cited=['R6'] hallucinated=[] valid=True
ANSWER: Based on the context provided, the thegratefulgirlcooks.com Fish and Chips recipe does not
list sulfites as an allergen caution. [R6] The allergen cautions for this recipe are listed as
"none listed."

REFINED (vector rerank, top 6 recipes; matching chunks in brackets):
  - [R1] Fish and chips | source=womensweeklyfood.com.au | tier=hot | cautions=['Sulfites'] | score=0.8134 | [Ingredients, Allergen cautions and labels, Method steps 7-8]
  - [R2] Chorizo And Shrimp Rice Recipe | source=Food Republic | tier=hot | cautions=['Sulfites'] | score=0.7646 | [Ingredients, Allergen cautions and labels]
  - [R3] Slow-Cooker Chicken Mole | source=Delish | tier=hot | cautions=['Sulfites'] | score=0.7643 | [Allergen cautions and labels]
  - [R4] The Best Homemade BBQ Sauce | source=everydaypartymag.com | tier=hot | cautions=['Sulfites'] | score=0.7621 | [Allergen cautions and labels]
  - [R5] Tamale Filling Recipe | source=Group Recipes | tier=hot | cautions=['Sulfites'] | score=0.7604 | [Allergen cautions and labels]
  - [R6] Fish and Chips | source=thegratefulgirlcooks.com | tier=lt | cautions=[] | score=0.7471 | [Ingredients, Allergen cautions and labels, Method step 7]
```

The pin follows from one fact: BM25 grounding put the named recipe at the top (tied with the other Fish and Chips recipe), and the rule trusts that. Without the pin, the evidence for this question would be the sulfite chunks of five other recipes and nothing from the recipe the user asked about.

**Combined with a hard filter**: `--exclude` and `--require` work as in Hybrid RAG (NER + Rerank), including its caveat that an unrecognised filter name (for example the typo `alergen`) is used as written and matches no field, so an `--exclude` on it silently removes nothing. `--exclude allergen=Sulfites` on a generic
"suggest a fish and chips recipe" question removes the hot-tier recipe from
the candidate pool entirely, which *also* triggers escalation (this time via
the generic low-confidence-score path, since the remaining hot-tier
candidates are all weak matches), and the answer recommends the Fish and Chips recipe from thegratefulgirlcooks.com:

```text
python -m src.query --question "Suggest a British fish and chips recipe." --exclude allergen=Sulfites
GOVERNANCE: tiers_searched=['hot', 'lt'] escalated=True reason=low_confidence_hot_score hot_hits=8 hot_top_score=6.9581
CITATIONS: evidence=['R1', 'R2', 'R3', 'R4', 'R5'] cited=['R1'] hallucinated=[] valid=True
ANSWER: I can suggest the **Fish and Chips recipe from thegratefulgirlcooks.com** [R1]...
```

Every call also writes one document to `recipes-audit-log`. Each record captures the
question, entities, filters, `tiers_searched`, `escalated`/`escalation_reason`,
the refined recipe IDs/sources/cautions, the `evidence_chunk_ids` behind them, any `pinned_named_sources`, `evidence_handles`/`cited_handles`/
`hallucinated_citations`/`citations_valid`, the answer, and latency, so you
can answer both "did this query reach the long-term tier, and why (not)?" and
"did the answer's citations actually match what was retrieved?" for any past
question without re-running anything. Operationalize Production reads these records back.

## Step 5: validate

With `ner_service.py` still running:

```bash
python -m src.validate
```

`validate.py` asserts four things (the two live checks fail up front, with a message, if the NER service isn't answering, because the clients otherwise hide that):
1. **Citation mechanism itself** (no live services needed): `[R#]` tags are
   parsed correctly out of a sample answer, and a citation for a handle that
   was never retrieved is correctly flagged as hallucinated.
2. **Source detection** (no live services needed): a question that names a
   source domain such as `thegratefulgirlcooks.com` yields that domain, while
   numbers and abbreviations such as "2.5" or "e.g" yield nothing, so they
   cannot trigger an escalation.
3. **Hot-tier recipe** resolves with `escalated=False`, `tiers_searched=["hot"]`,
   womensweeklyfood.com.au's recipe is among the results, and the live LLM answer cites at least one retrieved handle with no
   hallucinated citations (`citations_valid=True`).
4. **LT-tier recipe** resolves with `escalated=True`, `"lt" in tiers_searched`,
   the correct recipe is actually present in the final results, and again
   the live answer's citations are valid.

**Expected:** four `[PASS]` lines, as in this sample (the `meta` values, including `hot_top_score`, vary between runs; `escalated`, `escalation_reason`, `cited_handles`, and `citations_valid` should match):

```text
[PASS] Governance Tiering citation extraction and hallucination detection work as expected
[PASS] Governance Tiering source detection ignores numbers and abbreviations
[PASS] Governance Tiering answered the hot-tier recipe without escalating: meta={'tiers_searched': ['hot'], 'escalated': False, 'escalation_reason': None, 'hot_hit_count': 25, 'hot_top_score': 22.731224, 'evidence_handles': ['R1', 'R2', 'R3', 'R4', 'R5'], 'evidence_chunk_ids': ['a9d7ddb702fa-00', 'a9d7ddb702fa-01', 'a9d7ddb702fa-05', 'af8092753b21-00', 'af8092753b21-01', '1a538998cf8c-01', '17516aacf32f-01', 'e12eb6111f1e-01'], 'pinned_named_sources': [], 'cited_handles': ['R1'], 'hallucinated_citations': [], 'citations_valid': True}
[PASS] Governance Tiering escalated to the LT tier and found the correct recipe: meta={'tiers_searched': ['hot', 'lt'], 'escalated': True, 'escalation_reason': 'named_source_not_in_hot_tier', 'hot_hit_count': 25, 'hot_top_score': 28.731224, 'evidence_handles': ['R1', 'R2', 'R3', 'R4', 'R5', 'R6'], 'evidence_chunk_ids': ['a9d7ddb702fa-00', 'a9d7ddb702fa-01', 'a9d7ddb702fa-05', '7e8a943b5c0d-00', '7e8a943b5c0d-01', '64671cf8a9e6-01', '3c5eef70c11e-01', 'e12eb6111f1e-01', '9ccd03de3c8a-00', '9ccd03de3c8a-01', '9ccd03de3c8a-05'], 'pinned_named_sources': ['thegratefulgirlcooks.com'], 'cited_handles': ['R6'], 'hallucinated_citations': [], 'citations_valid': True}
```

## Completion checkpoint

You have completed Governance Tiering when ingest reported `hot=209, lt=51` over 1823 chunks, the hot-tier question resolved with `escalated=False`, the LT-tier question escalated with `reason=named_source_not_in_hot_tier`, pinned the named recipe and cited it, every `CITATIONS:` line showed `valid=True`, and all four smoke-test checks passed. Those queries also gave `recipes-audit-log` the records Operationalize Production monitors. The lesson to carry forward: a tiering rule is a governance decision, and the audit record is what lets you prove it was followed.

For how this step fits with the other approaches, and what each one adds and cannot do, see [Choosing a retrieval approach](../concepts/CHOOSING_RETRIEVAL.md). For what the NER step contributes and where it silently does nothing, see [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md). For how chunks, citation handles and the audit record fit together, see [Chunking and governance](../concepts/CHUNKING.md#chunking-and-governance).

The [Governance Tiering source map](../references/governance-tiering.md#source-map) lists every file used here. Setup or runtime issue? See [Governance Tiering troubleshooting](../troubleshooting/governance-tiering.md)
for common fixes -- including what to check if `ner_service.py` isn't
running (entity matching silently degrades to plain full-text matching
rather than erroring), missing `.env` values, certificate errors, and
LLM-provider model access.

Continue to [Task 7: Operationalize Production](operationalize-production.md), or return to the [task index](../README.md#tasks).
