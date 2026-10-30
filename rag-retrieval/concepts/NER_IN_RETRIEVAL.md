# Named-entity recognition in retrieval

[Return to Task 3: Baseline BM25 RAG](../tasks/baseline-bm25-rag.md) · [Task 5: Hybrid RAG (NER + Rerank)](../tasks/hybrid-rag-ner-rerank.md) · [Task 6: Governance Tiering](../tasks/governance-tiering.md) · [Task 7: Operationalize Production](../tasks/operationalize-production.md)

Tasks 3, 5 and 6 call a small local service before they search. The service reads the question and returns the names it finds in it, and the search gives a bonus to recipes that carry the same names. This page explains what that service does, shows on live data what it extracts and what the entity boost does to a ranking, lists the alternatives and when each one is the better tool, and explains what to watch in production.

## What this concept means

**Named-entity recognition (NER)** finds spans of text that name a person, an organisation, a place, a product and so on, and attaches a label to each span. It does not know what the names mean. "Heinz" is an organisation to the model; whether Heinz makes anything you can eat is not part of the output.

In this lab, NER is one function in [`ner_service.py`](../scripts/baseline-bm25-rag/src/ner_service.py): a Flask service that runs spaCy's `en_core_web_sm` model and answers `POST /ner` on `127.0.0.1:8000`. For a text it returns `entities` (names, lowercased, de-duplicated), `entity_pairs` (each name with its label) and the model name. The service keeps only nine labels (`PERSON`, `ORG`, `PRODUCT`, `GPE`, `EVENT`, `WORK_OF_ART`, `NORP`, `LOC`, `FAC`) and drops names shorter than three characters. Callers reach it through `NERClient` in [`ner_client.py`](../support/common/ner_client.py).

The entities are used in two places:

1. **At ingest**, each recipe's text is sent to the service. The returned names, plus the recipe's own `source` and `recipe_name`, are stored in the recipe's `entities` field. That field is a `keyword` field, so each stored name is matched as an exact string.
2. **At query time**, the question is sent to the service. The names that come back are added to the BM25 query as a `constant_score` clause on the `entities` field with a boost of 6.0. A recipe whose stored `entities` contain one of the question's names gets +6.0 on top of its text score. Nothing else about the ranking changes.

| Task | Calls the NER service | For |
| --- | --- | --- |
| 2 Baseline Vector RAG | No | |
| 3 Baseline BM25 RAG | Yes | Ingest tags and the query-time boost |
| 4 Hybrid RAG (Native) | No | Nothing: there is no `entities` field, and the source name is already part of the recipe text that BM25 searches |
| 5 Hybrid RAG (NER + Rerank) | Yes | Ingest tags and the grounding query's boost |
| 6 Governance Tiering | Yes | Same as Task 5, and the extracted entities are written to the audit record |
| 7 Operationalize Production | Yes | A health check only |

## Probe 1: what the service extracts from real questions

Run this from `scripts/baseline-bm25-rag/` with the `.venv` active and the NER service running (Task 3, Step 2). It posts nine questions to the service. It reads no index, calls no LLM and writes nothing.

```bash
python - <<'PY'
import requests
from src.common.config import load_settings

s = load_settings()
questions = [
    "Which Fish and Chips recipe from womensweeklyfood.com.au is gluten free?",
    "Does Heinz tomato ketchup contain sulfites?",
    "Which recipes from BBC Good Food are vegan?",
    "Show me recipes from Jamie Oliver",
    "Is the Chorizo And Shrimp Rice Recipe safe for a shellfish allergy?",
    "Which recipes from Food Network are free of sulfites?",
    "Which recipes from food network are free of sulfites?",
    "Does Food Network's Microwave Potato Chips recipe contain sulfites?",
    "What can I make with chicken and rice?",
]
for q in questions:
    pairs = requests.post(s.ner_service_url, json={"text": q}, timeout=10).json()["entity_pairs"]
    print(q)
    print("  ->", [(p["name"], p["label"]) for p in pairs] or "nothing extracted")
PY
```

**Expected:** each question followed by what the service returned.

```text
Which Fish and Chips recipe from womensweeklyfood.com.au is gluten free?
  -> [('chips', 'ORG'), ('womensweeklyfood.com.au', 'ORG')]
Does Heinz tomato ketchup contain sulfites?
  -> [('heinz', 'ORG')]
Which recipes from BBC Good Food are vegan?
  -> [('bbc good food', 'ORG')]
Show me recipes from Jamie Oliver
  -> [('jamie oliver', 'PERSON')]
Is the Chorizo And Shrimp Rice Recipe safe for a shellfish allergy?
  -> [('chorizo', 'PERSON'), ('shrimp rice recipe', 'PERSON')]
Which recipes from Food Network are free of sulfites?
  -> [('food network', 'ORG')]
Which recipes from food network are free of sulfites?
  -> nothing extracted
Does Food Network's Microwave Potato Chips recipe contain sulfites?
  -> [("food network's", 'ORG')]
What can I make with chicken and rice?
  -> nothing extracted
```

**Interpretation:** the service finds *names*, not food facts. "Shellfish", "sulfites", "vegan" and "gluten free" are never extracted. Allergens and diets are handled by the structured `cautions` and `diet_labels` fields and by the `--exclude` and `--require` filters in Task 5.

## Probe 2: what the boost does, and what it does not do

Run this from `scripts/baseline-bm25-rag/` after the Task 3 ingest. It runs one BM25 query twice, without and with the entity that Probe 1 shows NER extracts from this question, and passes the entity in directly. No LLM call and nothing is written.

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.opensearch_client import bm25_search, create_client, normalize_hits

s = load_settings()
client = create_client(s)
question = "Which recipes from Food Network are free of sulfites?"

for label, entities in [("BM25 only", []), ("BM25 + entity boost for 'food network'", ["food network"])]:
    hits = normalize_hits(bm25_search(client, s.opensearch_bm25_recipes_baseline_index, question, k=10, entities=entities))
    print(label)
    for h in hits[:5]:
        print(f"  {h['score']:7.4f}  {h['recipe_name'][:32]:32s} | {h['source']:18s} | {h['cautions_display']}")
    print(f"  Food Network recipes in the top 10: {sum(h['source'] == 'Food Network' for h in hits)}")
    print(f"  recipes in the top 10 that carry Sulfites: {sum('Sulfites' in h['cautions_display'] for h in hits)}")
PY
```

**Expected:** two lists, each with the first five hits and two counts.

```text
BM25 only
   8.2704  End-of-the-Rainbow Cookies       | Martha Stewart     | ['Soy', 'Sulfites']
   7.9419  Whole30 Ground Beef Taco Meat (K | tasteslovely.com   | ['Sulfites']
   7.6270  Saddle of lamb stuffed with rata | BBC Good Food      | []
   7.1755  Gluten Free Banana Muffins       | Honest Cooking     | ['Sulfites']
   6.5703  Strawberry-Banana Lactose Free P | Betty Crocker      | ['Sulfites']
  Food Network recipes in the top 10: 1
  recipes in the top 10 that carry Sulfites: 7
BM25 + entity boost for 'food network'
  11.2916  Microwave Potato Chips           | Food Network       | []
  11.1133  Castaway Punch                   | Food Network       | ['Sulfites']
  11.1132  Pink Puffed Rice Cereal Hearts   | Food Network       | ['Gluten', 'Wheat', 'Sulfites']
  10.8834  Easy White Pizza                 | Food Network       | ['Sulfites']
  10.8833  Cheesy Baked Pasta with Meatball | Food Network       | ['Sulfites']
  Food Network recipes in the top 10: 10
  recipes in the top 10 that carry Sulfites: 9
```

**Interpretation:** this is the case where the boost earns its place. On words alone, only one Food Network recipe reaches the top 10 (it is not among the first five shown). With the matching entity, all ten are Food Network recipes. The boost works as a *source selector*.

The second count is the limit. The question also says "free of sulfites", and the boosted list holds nine recipes that carry Sulfites, two more than before. NER extracted nothing about sulfites, and a boost is a preference in any case, not a rule. A question that must never return a sulfite recipe needs the hard filter from Task 5, not a bigger boost.

## Alternatives, and when to use each

The lab uses a local spaCy service for one reason: it teaches the mechanism with the least setup. Other choices exist. The table is based on how each approach works in general; none of the alternatives was run in this lab.

| Approach | What it does | Use it when | Costs and risks |
| --- | --- | --- | --- |
| **Dictionary (gazetteer) lookup** | Match the question against a list of known names (sources, recipe names, brands) | The names you care about are a closed, known set, as the recipe `source` values are here | You maintain the list; unknown names are missed; no label. Deterministic and easy to audit |
| **Rules and regular expressions** | Pattern-match shapes such as domain names | The thing you want has a fixed shape | Only finds that shape. Task 6 already does this for domains: `_extract_domain_tokens()` uses a regular expression because the small model is unreliable for them |
| **A larger spaCy model** (`en_core_web_md`, `lg` or `trf`) | The same task and labels with a bigger model | The labels are right and you only need better accuracy | A larger download and slower start; still general text, still no allergen concept. *Not run here* |
| **A zero-shot or open-label extractor** (for example GLiNER-style models) | You name the labels at run time, such as "ingredient" or "allergen" | You need entity types the general models do not have | A model to host and tune, and results to verify per label. *Not run here* |
| **An LLM extracts entities or filters** | Ask a language model to return structured fields | Questions are varied and phrased freely, and you can afford the delay | A model call per question; output can vary between runs; harder to audit than a rule. *Not run here* |
| **A managed NLP service** (for example a cloud entity API) | The same job as a hosted API | You already run on that cloud and do not want to host a model | Network latency, per-request cost, data leaves your host. *Not run here* |
| **No NER: structured metadata and filters** | Use the fields you already have (`source`, `cautions`, `diet_labels`) with `filter` and `must_not` | The question maps to fields you already store | Needs the user, or a UI, to supply the field values. Exact and auditable |

For the question "which recipes are free of sulfites?", the right tool is the last row, and the lab does exactly that in Task 5. NER is an option for *finding the names a user typed in free text*. It does not decide anything about safety.

## Why a local spaCy service

- **It runs on a laptop.** The model is about 13 MB, with no GPU and no account. See [Infrastructure choices](INFRASTRUCTURE_CHOICES.md) for the other pieces and where each one runs.
- **The output is deterministic.** The same text gives the same entities on one model version, which keeps probes like these repeatable.
- **It shows the failure mode.** A separate service that can be down is exactly what Task 7 teaches you to watch.

The cost is a second process to start before Tasks 3, 5 and 6.

## Failure and monitoring

`NERClient.extract_entities()` catches every failure and returns an empty list, with one logged warning. The design is deliberate: a flaky service should not take down search. The consequences are quiet:

- **At query time**, no entities means no boost. The search still runs and returns results, with the plain BM25 ranking. Hard filters (`--exclude`, `--require`) are unaffected.
- **At ingest**, a recipe indexed while the service is down is stored with only its `source` and `recipe_name` as entities, and keeps that smaller list until it is re-ingested.
- **Nothing errors.** There is no exception, and the answer still looks fine.

Three guards in the lab:

- `NERClient.is_available()` posts a trivial text and checks for a 200 response with a JSON object. The smoke tests of Tasks 3, 5 and 6 call it first, so they fail up front when the service is down instead of passing on degraded results.
- Task 7's `monitor.py` probes the real `/ner` endpoint each time it runs (the lab does not schedule it, so run it from your own scheduler) and raises an alert when it does not answer (see [Task 7](../tasks/operationalize-production.md#see-the-alert-logic-without-the-cluster)).
- Task 6 writes the extracted `entities` into every audit record, so an empty list on a question that obviously names something is visible after the fact.

Beyond the lab, you would also want a restart policy for the service, a real health route, and a version pin on the model, because a spaCy upgrade can change what is extracted and with it the scores.

## Tradeoffs and limits

- NER adds a process to run and, at best, a small ranking nudge for questions that name a source or a recipe. In this lab the nudge is useful in Probe 2. It does not decide whether the Fish and Chips pair is retrieved, which BM25 does from the recipe name alone (see [Task 3](../tasks/baseline-bm25-rag.md)).
- A boost expresses preference. A rule needs a filter. Do not rely on entities for anything that must always or never happen.
- The probes use the small English model and 260 recipes; results with another model, language or corpus will differ.
- Latency and memory of the service were not measured here.

## Related pages

- [BM25 versus kNN](BM25_VS_KNN.md): the keyword ranking that the entity boost is added to.
- [Choosing a retrieval approach](CHOOSING_RETRIEVAL.md): when you need entities, filters, hybrid search or governance.
- [Embedding models](EMBEDDING_MODELS.md): the other model in the lab, and what it measures.
- [Infrastructure choices](INFRASTRUCTURE_CHOICES.md): what runs where.
- [Architecture](../architecture.md): the retrieval progression task by task.
