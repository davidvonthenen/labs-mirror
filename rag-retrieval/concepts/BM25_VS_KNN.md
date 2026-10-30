# BM25 versus kNN, and how to choose between them

[Return to Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md) · [Task 3: Baseline BM25 RAG](../tasks/baseline-bm25-rag.md) · [Task 4: Hybrid RAG (Native)](../tasks/hybrid-rag-native.md)

Every task in this lab answers one question first: *which stored recipes should the model see?* There are two basic ways to rank recipes against a question, and they fail in opposite ways. This page explains what each one measures, shows both on the lab's own data, and gives you the rules for choosing, including why Task 4 uses the weights 0.3 and 0.7.

## What this concept means

**BM25** is a keyword ranking function. It splits the question and each document into words (the `recipe_name` and `text` fields here use OpenSearch's default analyzer: lowercase, split into words, no stemming). A document scores only if it contains at least one of the question's words. The score rises with how often the word appears in the document, falls for long documents, and rises for rare words. The rare-word part is the *inverse document frequency*: a word found in 201 of 260 recipes carries very little weight, and a word found in two recipes carries a lot.

**kNN (k nearest neighbours)** compares meaning. An embedding model turns the question and every recipe into a 384-number vector (see [Embedding models](EMBEDDING_MODELS.md)). Ranking is by the angle between the question's vector and each recipe's vector. Every recipe has a distance to the question, so kNN always returns `k` results, however weak the best match is.

| | BM25 | kNN over embeddings |
| --- | --- | --- |
| Compares | Words in the question with words in the document | Vectors learned from text |
| Finds a document when | At least one query word appears in it | Always (it returns the `k` closest, good or not) |
| Score | Unbounded; depends on the whole corpus | Bounded; OpenSearch reports cosine as `(1 + cosine) / 2` |
| Needs a model | No | Yes (here, `all-MiniLM-L6-v2` on CPU) |
| Explainable | Yes: you can name the matching words | No: you can only report a similarity |

## Probe 1: put the same queries through both retrievers

Run this from `scripts/baseline-bm25-rag/` with the `.venv` active, after the Task 2 and Task 3 ingests. It reads both baseline indexes, makes no LLM call and writes nothing.

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import bm25_search, create_client, normalize_hits, recipe_knn_search

s = load_settings()
client = create_client(s)
model = EmbeddingModel(s)

queries = [
    "thegratefulgirlcooks.com",                # an exact identifier
    "chorizo",                                 # an exact ingredient word
    "spicy Spanish sausage with rice",         # a description; the word chorizo never appears
    "sweet baked treat without wheat flour",   # a paraphrase that includes a negation
    "contains sulfites",                       # a word that 201 of the 260 recipes carry
]
for q in queries:
    bm25 = normalize_hits(bm25_search(client, s.opensearch_bm25_recipes_baseline_index, q, k=3))
    vec = to_list(model.encode([q])[0])
    knn = normalize_hits(recipe_knn_search(
        client, s.opensearch_vector_recipes_baseline_index, vec, k=3, candidate_k=25))
    print(f"\nQUERY: {q}")
    for h in bm25:
        print(f"  BM25    {h['score']:7.3f}  {h['recipe_name'][:48]}")
    for h in knn:
        print(f"  vector  {h['score']:7.3f}  {h['recipe_name'][:48]}")
PY
```

**Expected:** a model-loading log line and a progress bar on stderr, then five query blocks. Scores can differ slightly if you changed the sample or the model; the properties below should hold.

```text
QUERY: thegratefulgirlcooks.com
  BM25      4.594  Fish and Chips
  vector    0.550  Marry Me Chicken
  vector    0.549  Giggety! Spearing Frogs and Fried Frog Legs reci
  vector    0.549  Ladyfinger Lemon Dessert

QUERY: chorizo
  BM25      8.270  Chorizo Taquito with Cheddar Queso
  BM25      8.270  Chorizo And Shrimp Rice Recipe
  vector    0.763  Chorizo Taquito with Cheddar Queso
  vector    0.760  Chorizo And Shrimp Rice Recipe
  vector    0.693  Beef and Bok Choy

QUERY: spicy Spanish sausage with rice
  BM25     13.408  Chorizo And Shrimp Rice Recipe
  BM25     12.137  Spicy Salmon with Avocado Salsa
  BM25      9.949  Mushroom-Sausage Ragù
  vector    0.807  Chorizo And Shrimp Rice Recipe
  vector    0.768  Creole-Style Bean Soup
  vector    0.758  Mushroom-Sausage Ragù

QUERY: sweet baked treat without wheat flour
  BM25     10.835  Sweet And Sour Baked Chicken Legs Recipe
  BM25     10.835  Oven-Baked Spiced Sweet Potato Fries recipes
  BM25      7.904  Yummy Baked Beans
  vector    0.778  Whole wheat banana bread with brown butter
  vector    0.765  End-of-the-Rainbow Cookies
  vector    0.765  Low Cost Chocolate Cake

QUERY: contains sulfites
  BM25      0.302  Gingersnap Crumb Crust
  BM25      0.296  Egg-White Smoothie
  BM25      0.292  Haddock with Mustard Sauce
  vector    0.746  Summer Bolognese
  vector    0.745  Slow Cooker Garden Fresh Chili on Red Potatoes
  vector    0.740  Chocolate Rigo Squares
```

Read the output query by query:

- **An exact identifier.** BM25 returns one hit, the recipe that carries that domain in its text and `source` field. The vector search returns three recipes whose scores are almost identical (0.550, 0.549, 0.549) and none is related. An embedding model has no stable meaning for an unusual string, so it cannot find it. This is the same reason Task 2 loses the thegratefulgirlcooks.com recipe.
- **BM25 can return fewer than `k`.** It returned one hit for the identifier and two for "chorizo", because only that many recipes contain the word. kNN always fills `k`, which is why a vector search can look confident while returning nothing relevant.
- **A description.** Both rank the same recipe first, but for different reasons. BM25 can only have matched on words the question and the recipe text share, and its second hit ("Spicy Salmon") is there for the word "spicy". The vector ranks by closeness of meaning. Overlapping words can make BM25 look semantic when it is not.
- **A paraphrase with a negation.** BM25 matches "sweet" and "baked" in savoury recipes. The vector returns sweet baked goods, which is closer in meaning, but its first hit is a whole-wheat bread: "without wheat flour" did not stop it. Neither retriever understands "without". A requirement like that needs a structured filter (see Filters below).
- **A word almost every recipe carries.** BM25 scores top out at 0.30 because 201 of 260 recipes carry the Sulfites caution, so the word has almost no weight. The vector scores look high (0.746) but the recipes are unrelated to the question. Questions about a common attribute are not a ranking problem at all; they are a filtering problem.
- **The scores are on different scales.** BM25 reached 13.4 and the vector scores sit between 0.55 and 0.81. You cannot add them without rescaling, which is the job of the pipeline in Task 4.

## When each one wins

This table reflects the probe above plus general retrieval behaviour. Rows marked *general* were not measured in this lab.

| The question contains | Better first choice | Why |
| --- | --- | --- |
| A name, code, domain or other exact identifier | BM25 | The literal token matches; an embedding has no stable meaning for it (Probe 1, first query). |
| A description that avoids the document's own words | kNN | It matches meaning, not words (Probe 1, third and fourth queries). |
| A rare, specific keyword | BM25 | A rare word has a high inverse document frequency, so it dominates the score. |
| A word nearly every document carries | Neither; use a filter | BM25 gives it almost no weight; kNN returns unrelated documents (Probe 1, last query). |
| A negation or exclusion ("without", "no") | Neither; use a filter | Both treat the negated word as one more term or concept. |
| Misspellings and different word forms | kNN, with caveats (*general*) | The `text` field is not stemmed, so "sulfite" does not match "sulfites" in BM25. Not tested here. |
| Both an identifier and a description | Hybrid | Each side covers the other's blind spot (Task 4). |

## What "approximate" means in kNN

**Exact kNN** compares the question's vector with every stored vector and keeps the best `k`. The work grows in proportion to the number of documents. **Approximate kNN** (ANN) builds an index structure ahead of time so that it compares against only a small part of the data. This lab's vector indexes use **HNSW** (Hierarchical Navigable Small World) on the Lucene engine: a layered graph in which each vector is linked to nearby vectors, and a query walks the graph toward the closest region. It is fast, and it may occasionally miss a true nearest neighbour.

Two numbers control what you get:

- the pool size, which is the `k` inside the `knn` clause (named `candidate_k` in `build_recipe_vector_query()` in `support/common/opensearch_client.py`): how many neighbours the graph search collects;
- the response `size`: how many of those the query returns.

### Probe 2: how close is approximate to exact on this data

Run this from `scripts/baseline-vector-rag/` after the Task 2 ingest. It scores every vector in the index with OpenSearch's `knn_score` script and compares that exact top 10 with the approximate top 10, for 20 recipe names used as queries.

```bash
python - <<'PY'
import json

from src.common.config import RECIPES_SAMPLE_PATH, load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import create_client, normalize_hits, recipe_knn_search

s = load_settings()
client = create_client(s)
index = s.opensearch_vector_recipes_baseline_index
model = EmbeddingModel(s)


def exact_knn(vec, k):
    """Score every vector in the index (no HNSW graph) and keep the best k."""
    body = {"size": k, "_source": ["recipe_id"], "query": {"script_score": {
        "query": {"match_all": {}},
        "script": {"source": "knn_score", "lang": "knn",
                   "params": {"field": "embedding", "query_value": vec, "space_type": "cosinesimil"}}}}}
    return normalize_hits(client.search(index=index, body=body))


recipes = json.loads(RECIPES_SAMPLE_PATH.read_text(encoding="utf-8"))
queries = [r["recipe_name"] for r in recipes[:20]]
k, candidate_k = 10, 25
agree = 0
for q, vec in zip(queries, model.encode(queries)):
    vec = to_list(vec)
    exact_ids = {h["recipe_id"] for h in exact_knn(vec, k)}
    approx = normalize_hits(recipe_knn_search(client, index, vec, k=k, candidate_k=candidate_k))
    agree += len(exact_ids & {h["recipe_id"] for h in approx})
print(f"approximate (HNSW) results found by exact search: {agree}/{k * len(queries)}")
PY
```

**Expected:** one line, `approximate (HNSW) results found by exact search: 200/200`.

**Interpretation:** at 260 vectors the approximate search returned exactly the exact results. That is expected for a graph this small, and it means this lab cannot show you the accuracy cost of ANN. That cost appears with large indexes, where you trade a little recall for much lower latency and tune the pool size to buy some recall back. It was not measured here. For a corpus this size, ANN is a free default, not a decision.

## Filters: where they apply

A structured filter ("exclude recipes with the Sulfites caution") sounds simple, but where it runs changes the answer. In this lab's vector query, the `knn` clause sits inside `bool.must` and the filter sits outside it, so OpenSearch first collects the `candidate_k` nearest neighbours **across the whole index** and then discards the ones that fail the filter. This is a *post-filter*.

### Probe 3: a post-filter over a small pool

Run this from `scripts/baseline-vector-rag/` after the Task 2 ingest.

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import create_client, normalize_hits, recipe_knn_search

s = load_settings()
client = create_client(s)
index = s.opensearch_vector_recipes_baseline_index
question = "Does the womensweeklyfood.com.au Fish and Chips recipe contain sulfites?"
vec = to_list(EmbeddingModel(s).encode([question])[0])

unfiltered = recipe_knn_search(client, index, vec, k=25, candidate_k=300)
print(f"no filter: {len(normalize_hits(unfiltered))} hits")
for candidate_k in (10, 25, 100, 300):
    resp = recipe_knn_search(client, index, vec, k=25, candidate_k=candidate_k,
                             exclude={"cautions": ["Sulfites"]})
    print(f"exclude Sulfites, candidate_k={candidate_k:3d}: {len(normalize_hits(resp))} hits")
PY
```

**Expected:** five lines. The unfiltered query returns 25 hits. The filtered query returns 0 hits at `candidate_k` 10 and 25, 6 hits at 100, and 25 hits at 300.

**Interpretation:** the filter is applied correctly every time; what changes is how many neighbours were available to filter. The question mentions "sulfites", and 201 of the 260 recipes carry the Sulfites caution in their text, so the nearest neighbours of this question are mostly sulfite recipes, and none of the nearest 25 survives the exclusion. A user who sees "0 results" would reasonably conclude that no sulfite-free recipe exists, which is false. Three ways around it:

1. **Raise the pool** until it covers enough candidates. Task 6 does this with `REFINE_ANN_POOL` (300). It costs more work per query.
2. **Filter first, rank second.** Task 5 finds candidates with BM25 and the filters, then ranks only those by vector similarity.
3. **Move the filter inside the `knn` clause**, so the engine applies it while it searches the graph. OpenSearch supports this for some engines; this lab does not use it, and it was not tested here.

For a rule such as "never show an allergen", the first option's failure is silent, which is why a hard rule should not depend on the size of a candidate pool.

## Scores are not comparable

- **BM25 is unbounded and relative.** A score of 8.27 means nothing outside this index; adding documents changes every word's rarity and so every score.
- **The cosine score is bounded.** OpenSearch's `cosinesimil` space reports `(1 + cosine) / 2`, so cosine 0.653 is reported as 0.8265. Task 2's probe shows this on the Fish and Chips pair.
- **Fusing two lists needs a common scale.** Task 4 rescales each list with *min-max normalization*: the best hit of a list becomes 1.0 and the worst becomes 0.0. This is why Task 4's sample scores are 0.7000 and 0.3000: the top vector hit gets `1.0 x 0.7` and the top BM25 hit gets `1.0 x 0.3`. A fused score tells you rank within this one result set, not how good the match is. Do not use it as a confidence value or compare it across questions.

## Top-k: how many hits the model sees

`top_k` controls how many hits reach the prompt. Tasks 2, 4, 5 and 6 default to 5 hits and Task 3 to 10; each hit contributes up to 900 characters (1,200 in Task 3). Raising `k` is the obvious way to rescue a missed document, so measure what it would cost.

### Probe 4: how deep the missed recipe sits

Run this from `scripts/baseline-vector-rag/` after the Task 2 ingest.

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import create_client, normalize_hits, recipe_knn_search

s = load_settings()
client = create_client(s)
model = EmbeddingModel(s)
question = "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?"
vec = to_list(model.encode([question])[0])
hits = normalize_hits(recipe_knn_search(
    client, s.opensearch_vector_recipes_baseline_index, vec, k=260, candidate_k=260))
for rank, h in enumerate(hits, start=1):
    if h["source"] == "thegratefulgirlcooks.com" and h["recipe_name"].lower() == "fish and chips":
        print(f"rank {rank} of {len(hits)}  score={h['score']:.4f}")
PY
```

**Expected:** `rank 41 of 260  score=0.7597`.

**Interpretation:** the recipe Task 2 misses is the 41st nearest. To rescue it by raising `k` you would send 41 recipes (about 8 times the default 5) to the model, mostly irrelevant, and the same trick fails for the next question with a different miss. A larger `k` costs tokens and gives the model more look-alike distractors; it is not a substitute for a retriever that finds the right document. The right `k` is the smallest one at which the answer-bearing recipe is usually present, measured on questions you care about.

## Choosing between them

Start from the failure you must avoid, not from the technique:

1. **Identifier or exact-name questions** need BM25 (or a filter on a keyword field). Vector search cannot be trusted with them.
2. **Open-ended or descriptive questions** need vectors. BM25 only finds them when words happen to overlap.
3. **Both kinds in one product** need hybrid fusion, with the weights treated as a setting to evaluate (next section).
4. **A rule that must always hold** ("exclude this allergen", "only this tier") must be a structured filter applied so that it cannot silently empty or leak the result, not a property of ranking.

## Hybrid fusion and the weights 0.3 and 0.7

Task 4's search pipeline computes `0.3 x normalized BM25 + 0.7 x normalized vector` for each document. **These numbers were not derived or tuned for this lab.** They are a conventional meaning-first default: vector gets most of the vote, and BM25 gets enough to pull in exact matches. The probes below show what the weight does on this data so you can judge whether it fits your questions.

Run both from `scripts/hybrid-rag-native/` after the Task 4 ingest. They send each query with an inline search pipeline in the request, so the cluster-wide `hybrid-search-pipeline` is not changed. Probe 5 is the same sweep that [Task 4 runs](../tasks/hybrid-rag-native.md#why-03-and-07) in its Step 3; it is repeated here so this page reads on its own.

### Probe 5: sweep the weight on the Fish and Chips pair

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import VECTOR_FIELD, create_client

s = load_settings()
client = create_client(s)
embedder = EmbeddingModel(s)


def fused_hits(question, vec, bm25_weight, size=5):
    """Run the same hybrid query with an inline pipeline, so the cluster-wide pipeline is untouched."""
    body = {
        "size": size, "_source": ["recipe_name", "source"],
        "query": {"hybrid": {"queries": [
            {"match": {"text": question}},
            {"knn": {VECTOR_FIELD: {"vector": vec, "k": size}}},
        ]}},
        "search_pipeline": {"phase_results_processors": [{"normalization-processor": {
            "normalization": {"technique": "min_max"},
            "combination": {"technique": "arithmetic_mean",
                            "parameters": {"weights": [bm25_weight, round(1 - bm25_weight, 1)]}}}}]},
    }
    return client.search(index=s.opensearch_hybrid_index, body=body)["hits"]["hits"]


LABEL = {"thegratefulgirlcooks.com": "A", "womensweeklyfood.com.au": "B"}
for source in LABEL:
    question = f"Does the {source} Fish and Chips recipe contain sulfites?"
    vec = to_list(embedder.encode([question])[0])
    print(f"\nQuestion names recipe {LABEL[source]} ({source})")
    for bm25_weight in (0.0, 0.1, 0.2, 0.3, 0.5, 0.7, 0.9, 1.0):
        place = {}
        for rank, hit in enumerate(fused_hits(question, vec, bm25_weight), start=1):
            if hit["_source"]["source"] in LABEL:
                place[LABEL[hit["_source"]["source"]]] = f"#{rank} ({hit['_score']:.3f})"
        print(f"  bm25={bm25_weight:.1f} vector={1 - bm25_weight:.1f}   "
              f"A: {place.get('A', 'absent'):12s} B: {place.get('B', 'absent')}")
PY
```

**Expected:** two tables of eight rows.

```text
Question names recipe A (thegratefulgirlcooks.com)
  bm25=0.0 vector=1.0   A: absent       B: #1 (1.000)
  bm25=0.1 vector=0.9   A: #4 (0.100)   B: #1 (0.900)
  bm25=0.2 vector=0.8   A: #3 (0.200)   B: #1 (0.800)
  bm25=0.3 vector=0.7   A: #2 (0.300)   B: #1 (0.700)
  bm25=0.5 vector=0.5   A: #2 (0.500)   B: #1 (0.500)
  bm25=0.7 vector=0.3   A: #1 (0.700)   B: #2 (0.300)
  bm25=0.9 vector=0.1   A: #1 (0.900)   B: #3 (0.100)
  bm25=1.0 vector=0.0   A: #1 (1.000)   B: absent

Question names recipe B (womensweeklyfood.com.au)
  bm25=0.0 vector=1.0   A: absent       B: #1 (1.000)
  bm25=0.1 vector=0.9   A: absent       B: #1 (1.000)
  bm25=0.2 vector=0.8   A: absent       B: #1 (1.000)
  bm25=0.3 vector=0.7   A: absent       B: #1 (1.000)
  bm25=0.5 vector=0.5   A: absent       B: #1 (1.000)
  bm25=0.7 vector=0.3   A: absent       B: #1 (1.000)
  bm25=0.9 vector=0.1   A: absent       B: #1 (1.000)
  bm25=1.0 vector=0.0   A: absent       B: #1 (1.000)
```

Read it as follows:

- **Any BM25 weight above 0 rescues recipe A.** The weight changes where A lands, not whether it appears: fourth at 0.1, third at 0.2, second from 0.3 up to 0.5. The sample 0.7000 and 0.3000 in Task 4 is the 0.3 row.
- **The weight cannot make A rank first until BM25 outweighs vector.** At 0.5 the two scores tie at 0.500 and the order between them is arbitrary, and A is first only from 0.7. That is why Task 4's answer comes from the `source:` label printed with each snippet and not from the rank.
- **Recipe B's question is insensitive to the weight**, because both signals already agree on B.
- **The scores are the weights.** At 0.3 the two Fish and Chips scores are exactly 0.700 and 0.300. Each is the top hit of one list (normalized to 1.0) times its weight, and the recipe adds nothing from the other list, where it is either missing or at the bottom (min-max gives the bottom hit 0.0). See Scores above.

### Probe 6: the cost of a high BM25 weight

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import VECTOR_FIELD, create_client

s = load_settings()
client = create_client(s)
embedder = EmbeddingModel(s)


def fused_hits(question, vec, bm25_weight, size=5):
    body = {
        "size": size, "_source": ["recipe_name"],
        "query": {"hybrid": {"queries": [
            {"match": {"text": question}},
            {"knn": {VECTOR_FIELD: {"vector": vec, "k": size}}},
        ]}},
        "search_pipeline": {"phase_results_processors": [{"normalization-processor": {
            "normalization": {"technique": "min_max"},
            "combination": {"technique": "arithmetic_mean",
                            "parameters": {"weights": [bm25_weight, round(1 - bm25_weight, 1)]}}}}]},
    }
    return client.search(index=s.opensearch_hybrid_index, body=body)["hits"]["hits"]


question = "sweet baked treat without wheat flour"
vec = to_list(embedder.encode([question])[0])
print(f"QUESTION: {question}")
for bm25_weight in (0.0, 0.3, 0.5, 0.7, 1.0):
    names = [h["_source"]["recipe_name"][:30] for h in fused_hits(question, vec, bm25_weight)[:3]]
    print(f"  bm25={bm25_weight:.1f} vector={1 - bm25_weight:.1f}   " + " | ".join(names))
PY
```

**Expected:** five lines, one per weight.

```text
QUESTION: sweet baked treat without wheat flour
  bm25=0.0 vector=1.0   Whole wheat banana bread with  | End-of-the-Rainbow Cookies | Low Cost Chocolate Cake
  bm25=0.3 vector=0.7   Whole wheat banana bread with  | Sweet And Sour Baked Chicken L | Oven-Baked Spiced Sweet Potato
  bm25=0.5 vector=0.5   Whole wheat banana bread with  | Sweet And Sour Baked Chicken L | Oven-Baked Spiced Sweet Potato
  bm25=0.7 vector=0.3   Sweet And Sour Baked Chicken L | Oven-Baked Spiced Sweet Potato | Crispy Baked Pork Chops with T
  bm25=1.0 vector=0.0   Sweet And Sour Baked Chicken L | Oven-Baked Spiced Sweet Potato | Crispy Baked Pork Chops with T
```

**Interpretation:** the weights pull in opposite directions. The identifier question in Probe 5 wants BM25 at 0.7 or more before A is first, and this description wants BM25 low: from 0.3 a savoury chicken recipe is already second, and from 0.7 the sweet baked goods are gone. No single weight is best for both. 0.3 and 0.7 is a compromise that keeps the exact match in the results (second place) and still returns mostly sensible recipes for a description, and the compromise is already leaking at 0.3. Neither the pair nor this one question is a benchmark.

To choose weights for a real product, do not argue from two questions. Collect 20 to 50 questions with the recipes that should answer them, run a sweep like Probe 5 over all of them, and pick the weight (or a different fusion method) by the share of questions whose right recipe is in the top `k`. Re-run the sweep when you change the embedding model, the text you embed, or the corpus, because all three change the score distributions.

## Tradeoffs and limits

- The probes use one small corpus, one embedding model and a handful of queries. They show *how* the retrievers fail and how the weight behaves, not how often.
- BM25 here uses the default analyzer on `recipe_name` and `text` without stemming or synonyms. Adding either would change the BM25 results above.
- Exact versus approximate agreement (Probe 2) holds for 260 vectors only.
- Fusion improves what is *retrieved*; it does not make the model's answer correct, and it does not turn ranking into a rule. For rules, use filters and the governance steps in Tasks 5 to 7.

## Related pages

- [Embedding models](EMBEDDING_MODELS.md): what the vector side actually measures, and the limits behind the probes above.
- [Chunking](CHUNKING.md): what goes into the text that both retrievers index.
- [Infrastructure choices](INFRASTRUCTURE_CHOICES.md): why the lab uses one hosted OpenSearch cluster.
- [Architecture](../architecture.md): the retrieval progression task by task (section 8).
