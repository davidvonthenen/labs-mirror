# Task 3: Baseline BM25 RAG

Builds on [Baseline Vector RAG](baseline-vector-rag.md), on the same
"Recipe & Dietary Safety Assistant" dataset.

This task uses **named-entity recognition (NER)**: a small local service that picks the names out of a question, so the search can give a bonus to recipes that carry the same names. [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md) explains what it does, what it extracts on this data, and when it helps. You can read it first or after the task. For why keyword (BM25) and vector (kNN) search fail in different ways, see [BM25 versus kNN](../concepts/BM25_VS_KNN.md).

**The fix, and what it costs.** BM25 matches the literal words in a recipe,
so the recipe that vector search dropped comes back, and the answers are
correct. Entity matching adds a boost on top, at the cost of a second service
to run. Here it is applied to the theme where a missed allergen actually
matters.

**Teaching goal:** BM25 + entity tagging is deterministic and auditable --
the same query keyword either matches a document's text or it doesn't. This
task's job is to retrieve both of the same-named recipes that Baseline Vector
RAG's vector search could confuse, so the model can answer about the one the
question names.

## What you will learn

By the end, you will have run a local NER service, indexed recipes with entity tags, and seen exact-term matching retrieve both Fish and Chips recipes for each question. You will also see the cost: a second service has to stay running. The entity extraction is [`ner()` in `ner_service.py`](../scripts/baseline-bm25-rag/src/ner_service.py); the query is `bm25_search()`, called from [`ask()`](../scripts/baseline-bm25-rag/src/query.py).

The practical sequence is:

| Step | Activity | Checkpoint |
| --- | --- | --- |
| [1](#step-1-enter-the-existing-workspace) | Re-enter the workspace and open the task directory. | `Makefile` and `src/` are listed. |
| [2](#step-2-start-the-ner-service) | Start the NER service in Terminal 1. | Flask reports it is serving on `127.0.0.1:8000`. |
| [3](#step-3-ingest-the-recipe-sample) | Chunk, tag and index the sample in Terminal 2. | The log reports `Ingested 260 recipes as 1823 chunks into 'recipes-bm25-baseline'`. |
| [4](#step-4-ask-both-fish-and-chips-questions) | Ask about each same-named recipe. | Each answer names the correct recipe's sulfite status. |
| [5](#step-5-validate) | Run the smoke test with NER still running. | It prints two `[PASS] Baseline BM25 RAG ...` lines. |

## Understand BM25 and entity matching

### Architecture

![Baseline BM25 RAG architecture diagram](../images/baseline-bm25-rag/diagram.png)

### Dataset

Same curated 260-recipe sample as Baseline Vector RAG, in `support/recipes/recipes_sample.json`, with the same synthetic page text and the same chunks (1,823 of them; see [Why this lab chunks](baseline-vector-rag.md#why-this-lab-chunks))
-- including the same real, verified ambiguous pair: two "Fish and Chips"
recipes (thegratefulgirlcooks.com, no cautions vs. womensweeklyfood.com.au,
Sulfites) where Baseline Vector RAG's vector search demonstrably drops the first
recipe from its top-5 results entirely.

Each recipe is tagged at ingest time with entities from the local NER
service, plus its own `source` and `recipe_name` folded directly into the
same `entities` field. NER runs once per recipe on its facts text (name, source, ingredients, cautions and labels), and the same `entities` are stored on every chunk of that recipe, so an entity boost lifts all of a recipe's chunks together. This is a deliberate design choice, confirmed by
testing: spaCy's small NER model does **not** tag the domain
"thegratefulgirlcooks.com" at all (it tags unrelated words such as
"allergen" instead), so relying on NER alone would not have disambiguated
these two recipes. The source is already known, deterministic metadata --
there's no reason to leave a disambiguation signal you already have to
chance.

A note on what that does and does not do at query time, checked against the
live index. The entity boost only fires for entities NER extracts from the
*question*. In these two questions the source domain is not one of them, so
the folded-in source never matches. What retrieves both recipes is plain BM25 on the recipe
name ("Fish and Chips"), which both recipes share. Both questions therefore
return the *same* two top hits, tied on score, in no fixed order; the model
tells the recipes apart from the `source:` label printed beside each snippet
in its context. What NER does return depends on the question and on the
spaCy model version. With the pinned versions it returns `fish and chips` for
the thegratefulgirlcooks.com question and `chips` for the womensweeklyfood.com.au
one. The first matches the recipe *name* that ingest folded in, which both
recipes share, so both recipes get the same +6.0 and still tie. The second
matches no stored name, so nothing is boosted. With the NER service stopped,
both recipes still come back, tied at 22.73.

## Before you begin

Complete [Task 1: Prepare the workspace](setup-environment.md), including the `en_core_web_sm` download. Baseline Vector RAG is not a prerequisite, but its results are what this task is compared against.

This task uses two terminals, both with the `.venv` active and both in `scripts/baseline-bm25-rag/`. The NER service listens only on `127.0.0.1`, on port 8000 unless `PORT` is set. Each `python -m src.<module>` command has a matching Make target, listed in the [Baseline BM25 RAG reference](../references/baseline-bm25-rag.md#commands).

## Step 1: enter the existing workspace

In each of two terminals, from the lab root:

```bash
source .venv/bin/activate
cd scripts/baseline-bm25-rag
ls
```

**Expected:** `Makefile` and `src` in both terminals.

## Step 2: start the NER service

Terminal 1 (NER service, stays running):

```bash
python -m src.ner_service
```

**Expected:** Flask's startup message, including `Running on http://127.0.0.1:8000`. Leave this terminal open; Hybrid RAG (NER + Rerank), Governance Tiering and Operationalize Production reuse the same service. If the port is taken, see [Baseline BM25 RAG troubleshooting](../troubleshooting/baseline-bm25-rag.md).

## Step 3: ingest the recipe sample

Terminal 2:

```bash
python -m src.ingest
```

**Expected:** a log line `Ingested 260 recipes as 1823 chunks into 'recipes-bm25-baseline'`. If the index has the older one-document-per-recipe layout, ingest logs a `WARNING`, deletes it and creates it again for chunks. Add `--recreate` to rebuild an index that already holds chunks. If NER warnings (`NER request failed ...`) appear, the service from Step 2 isn't reachable, and recipes are indexed without NER entities.

## Step 4: ask both Fish and Chips questions

Still in Terminal 2:

```bash
python -m src.query --question "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?"
python -m src.query --question "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?"
```

**Expected:** each command prints the question, the model's answer, and a `RAG Hits` list of recipes (10 by default), each with its best chunk's score and, in brackets, the sections that matched. The wording varies from run to run; check these properties:

- **thegratefulgirlcooks.com question:** the answer is "no sulfites", and that recipe is among the hits. Baseline Vector RAG's vector search couldn't even retrieve it.
- **womensweeklyfood.com.au question:** the answer is "yes, sulfites".
- **Both questions:** the top two hits are the two Fish and Chips recipes, tied on score and in either order (about 28.7 for the thegratefulgirlcooks.com question, about 22.7 for the womensweeklyfood.com.au one), then unrelated recipes below 9.

The default here is 10 hits because `--top-k` defaults to 10 in this task; the later tasks default to 5. BM25 has no neighbour pool, so there is no `candidate_k` to set. It scores chunks, so `ask()` requests `chunk_fetch(10)` = 60 chunks, and `group_by_recipe()` folds them into the top 10 recipes.

BM25 closes the gap Baseline Vector RAG exposed because it matches the literal words "Fish", "and" and "Chips" in the recipe name (which every chunk repeats in its prefix) instead of relying on "similar meaning", so both same-named recipes are retrieved. The ranking does not tell the two apart (see the note under Dataset); the `source:` label on each snippet does, and the model answers about the source your question names.

### See the ranking without the model

This probe runs the retrieval step of `ask()` for the thegratefulgirlcooks.com question, grouping chunks into recipes as `ask()` does, once without the entity boost and once with the `fish and chips` entity that NER extracts from this question. It passes the entity directly instead of calling the NER service, so the NER service does not need to be running. No LLM is called and nothing is written.

```bash
python - <<'PY'
from src.common.chunking import chunk_fetch, group_by_recipe
from src.common.config import load_settings
from src.common.opensearch_client import bm25_search, create_client, normalize_hits

s = load_settings()
client = create_client(s)
question = "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?"

for label, entities in [("BM25 only", []), ("BM25 + entity boost for 'fish and chips'", ["fish and chips"])]:
    resp = bm25_search(client, s.opensearch_bm25_recipes_baseline_index, question, k=chunk_fetch(5), entities=entities)
    print(label)
    for h in group_by_recipe(normalize_hits(resp), top_k=5):
        print(f"  {h['score']:7.4f}  {h['recipe_name']} | {h['source']}  [{h['matched_chunks']} chunks matched]")
PY
```

**Expected:** two lists of five recipes.

```text
BM25 only
  22.7312  Fish and Chips | thegratefulgirlcooks.com  [7 chunks matched]
  22.7312  Fish and chips | womensweeklyfood.com.au  [7 chunks matched]
   8.9038  Microwave Potato Chips | Food Network  [7 chunks matched]
   8.0840  Weight Watchers Potato Chips | food.com  [7 chunks matched]
   7.7700  Lemon-Garlic Goat Cheese Dip Recipe | The Daily Meal  [1 chunks matched]
BM25 + entity boost for 'fish and chips'
  28.7312  Fish and Chips | thegratefulgirlcooks.com  [7 chunks matched]
  28.7312  Fish and chips | womensweeklyfood.com.au  [7 chunks matched]
   8.9038  Microwave Potato Chips | Food Network  [7 chunks matched]
   8.0840  Weight Watchers Potato Chips | food.com  [7 chunks matched]
   7.7700  Lemon-Garlic Goat Cheese Dip Recipe | The Daily Meal  [1 chunks matched]
```

The two Fish and Chips recipes tie at 22.7312 on words alone: every chunk starts with its recipe's name, so each of their seven chunks matches the question's "Fish and Chips" equally well, and the name field carries a weight of 4. The `fish and chips` boost adds exactly 6.0 to both of them, because both recipes store that name (ingest folds in each recipe's name), so the tie stays and nothing else changes. The ranking never separates the two recipes by the question's domain name, which is why the answers rely on the `source:` label. Every hit below the pair is a different dish that shares one word ("Chips") with the question. For when keyword matching wins or fails against vector search, see [BM25 versus kNN](../concepts/BM25_VS_KNN.md).

### See what the NER service extracts

The boost above only happens if the NER service returns an entity for the question and that entity equals a name stored on a recipe. This probe asks the running NER service about six questions and counts the recipes (not chunks) whose stored `entities` contain what came back. It needs the service from Step 2, reads the index, calls no LLM and writes nothing.

```bash
python - <<'PY'
import requests
from src.common.config import load_settings
from src.common.opensearch_client import create_client

s = load_settings()
client = create_client(s)
questions = [
    "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?",
    "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?",
    "Which recipes from Food Network are free of sulfites?",
    "Which recipes from food network are free of sulfites?",
    "Does Food Network's Microwave Potato Chips recipe contain sulfites?",
    "Is the Chorizo And Shrimp Rice Recipe safe for a shellfish allergy?",
]
for q in questions:
    pairs = requests.post(s.ner_service_url, json={"text": q}, timeout=10).json()["entity_pairs"]
    names = [p["name"] for p in pairs]
    n = 0
    if names:
        body = {"size": 0, "query": {"terms": {"entities": names}}, "aggs": {"recipes": {"cardinality": {"field": "recipe_id"}}}}
        n = client.search(index=s.opensearch_bm25_recipes_baseline_index, body=body)["aggregations"]["recipes"]["value"]
    print(q)
    print("  NER:", [(p["name"], p["label"]) for p in pairs])
    print("  recipes whose stored entities match:", n)
PY
```

**Expected:** each question, what NER returned, and how many recipes it can boost.

```text
Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?
  NER: [('fish and chips', 'WORK_OF_ART')]
  recipes whose stored entities match: 2
Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?
  NER: [('chips', 'ORG')]
  recipes whose stored entities match: 0
Which recipes from Food Network are free of sulfites?
  NER: [('food network', 'ORG')]
  recipes whose stored entities match: 16
Which recipes from food network are free of sulfites?
  NER: []
  recipes whose stored entities match: 0
Does Food Network's Microwave Potato Chips recipe contain sulfites?
  NER: [("food network's", 'ORG'), ('microwave potato chips', 'ORG')]
  recipes whose stored entities match: 1
Is the Chorizo And Shrimp Rice Recipe safe for a shellfish allergy?
  NER: [('chorizo', 'ORG'), ('shrimp rice recipe', 'PERSON')]
  recipes whose stored entities match: 1
```

The first two are the Step 4 questions. The first returns `fish and chips`, which both Fish and Chips recipes store (ingest folds in the recipe name), so both get the +6.0 and stay tied. The second returns `chips`, which no recipe stores, so nothing is boosted. Neither question's source domain is extracted. The other lines show what the service returns for other questions and how many stored recipes each result can boost. For what the entity boost does to a ranking, and the alternatives to this service, see [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md).

## Step 5: validate

With `ner_service.py` still running:

```bash
python -m src.validate
```

**Expected:** two lines: `[PASS] Baseline BM25 RAG NER service is running`, then `[PASS] Baseline BM25 RAG retrieved both Fish and Chips recipes for both questions and answered each`. [`validate.py`](../scripts/baseline-bm25-rag/src/validate.py) fails at the first check if the NER service isn't answering (the clients otherwise hide that), and checks that both recipes are among the hits for both questions; Step 4 is where you confirm the answers are correct.

## Completion checkpoint

You have completed Baseline BM25 RAG when ingest reported 260 recipes as 1823 chunks, the two questions produced "no sulfites" and "yes, sulfites" respectively, and both smoke-test checks passed with the NER service running. The lesson to carry forward: matching literal words retrieves both candidates, but the ranking does not tell them apart, and the NER service you had to run was not what made retrieval work for this pair. It is an extra moving part that can fail silently.

For when keyword matching beats vector search and when it does not, see [BM25 versus kNN](../concepts/BM25_VS_KNN.md); for what the NER service contributes, see [Named-entity recognition in retrieval](../concepts/NER_IN_RETRIEVAL.md); to compare all the approaches in the lab, see [Choosing a retrieval approach](../concepts/CHOOSING_RETRIEVAL.md). The [Baseline BM25 RAG source map](../references/baseline-bm25-rag.md#source-map) lists every file used here. Setup or runtime issue? See [Baseline BM25 RAG troubleshooting](../troubleshooting/baseline-bm25-rag.md)
for common fixes -- including what to check if `ner_service.py` isn't
running (entity matching silently degrades to plain full-text matching
rather than erroring), missing `.env` values, certificate errors, and
Bedrock model access.

Continue to [Task 4: Hybrid RAG (Native)](hybrid-rag-native.md), or return to the [task index](../README.md#tasks).
