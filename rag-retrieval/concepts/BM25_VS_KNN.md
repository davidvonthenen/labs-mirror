# BM25 versus kNN, and how to choose between them

[Return to Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md) · [Task 3: Baseline BM25 RAG](../tasks/baseline-bm25-rag.md) · [Task 4: Hybrid RAG (Native)](../tasks/hybrid-rag-native.md)

Every task in this lab answers one question first: *which stored recipes should the model see?* There are two basic ways to rank recipes against a question, and they fail in opposite ways. This page explains what each one measures, shows both on the lab's own data, and gives you the rules for choosing, including how Task 4 sets its weights.

## What this concept means

**BM25** is a keyword ranking function. It splits the question and each document into words (the `recipe_name` and `text` fields here use OpenSearch's default analyzer: lowercase, split into words, no stemming). A document scores only if it contains at least one of the question's words. The score rises with how often the word appears in the document, falls for long documents, and rises for rare words. The rare-word part is the *inverse document frequency*: a word found in 201 of the 1,823 chunks (about 11%) carries far less weight than a word found in only two chunks. (The index holds chunks, so BM25 counts chunks, not recipes.)

**kNN (k nearest neighbours)** compares meaning. An embedding model turns the question and every recipe chunk into a 384-number vector (see [Embedding models](EMBEDDING_MODELS.md)). Ranking is by the angle between the question's vector and each chunk's vector. Every chunk has a distance to the question, so kNN always returns `k` results, however weak the best match is.

In this lab both retrievers rank **chunks**, the pieces each recipe is split into (see [Chunking](CHUNKING.md)), and the lab folds the ranked chunks back into recipes. The probes below do the same with `group_by_recipe()`, so every hit is a recipe, scored by its best chunk.

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
from src.common.chunking import chunk_fetch, group_by_recipe
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
    "contains sulfites",                       # "sulfites" is in 201 of the 1,823 chunks
]
n = chunk_fetch(3)  # both searches return chunks; group_by_recipe folds them into recipes
for q in queries:
    bm25 = group_by_recipe(normalize_hits(
        bm25_search(client, s.opensearch_bm25_recipes_baseline_index, q, k=n)), top_k=3)
    vec = to_list(model.encode([q])[0])
    knn = group_by_recipe(normalize_hits(recipe_knn_search(
        client, s.opensearch_vector_recipes_baseline_index, vec, k=n, candidate_k=60)), top_k=3)
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
  BM25      6.470  Fish and Chips
  vector    0.601  Marry Me Chicken
  vector    0.578  Giggety! Spearing Frogs and Fried Frog Legs reci
  vector    0.578  Ladyfinger Lemon Dessert

QUERY: chorizo
  BM25      8.605  Chorizo Taquito with Cheddar Queso
  BM25      8.605  Chorizo And Shrimp Rice Recipe
  vector    0.805  Chorizo Taquito with Cheddar Queso
  vector    0.791  Chorizo And Shrimp Rice Recipe
  vector    0.717  Beef and Bok Choy

QUERY: spicy Spanish sausage with rice
  BM25     15.938  Chorizo And Shrimp Rice Recipe
  BM25     12.735  Spicy Salmon with Avocado Salsa
  BM25     10.350  Mushroom-Sausage Ragù
  vector    0.816  Chorizo And Shrimp Rice Recipe
  vector    0.781  Mushroom-Sausage Ragù
  vector    0.776  Creole-Style Bean Soup

QUERY: sweet baked treat without wheat flour
  BM25     11.045  Sweet And Sour Baked Chicken Legs Recipe
  BM25     11.045  Oven-Baked Spiced Sweet Potato Fries recipes
  BM25      9.427  Chocolate Chip Banana Muffins
  vector    0.754  No-Bake Chocolate Chip Cookie Bites recipes
  vector    0.748  Healthier Classic Peanut Butter Cookies
  vector    0.748  Whole wheat banana bread with brown butter

QUERY: contains sulfites
  BM25      2.667  Cheese Ball Snack Mix Recipe 4
  BM25      2.642  Gallo Pinto recipes
  BM25      2.642  Gingersnap Crumb Crust
  vector    0.746  Cherry Tuiles
  vector    0.743  Summer Bolognese
  vector    0.743  Chocolate Rigo Squares
```

Read the output query by query:

- **An exact identifier.** BM25 returns one hit, the recipe that carries that domain in its text and `source` field. The vector search returns three recipes (scores 0.601, 0.578, 0.578) and none is related. An embedding model has no stable meaning for an unusual string, so it cannot find it. This is the same reason Task 2 loses the thegratefulgirlcooks.com recipe.
- **BM25 can return fewer than `k`.** It returned one recipe for the identifier and two for "chorizo", because only that many recipes contain the word. kNN always fills `k`, which is why a vector search can look confident while returning nothing relevant.
- **A description.** Both rank the same recipe first, but for different reasons. BM25 can only have matched on words the question and the recipe text share, and its second hit ("Spicy Salmon") is there for the word "spicy". The vector ranks by closeness of meaning. Overlapping words can make BM25 look semantic when it is not.
- **A paraphrase with a negation.** BM25 matches "sweet" and "baked" in savoury recipes. The vector returns sweet baked goods and cookies, which is closer in meaning (though its first hit is a *no-bake* cookie), and its third hit is a whole-wheat bread: "without wheat flour" did not stop it. Neither retriever understands "without". A requirement like that needs a structured filter (see Filters below).
- **A word that many recipes carry.** BM25's best score is only 2.67, against 6.5 to 15.9 for the other queries. The only word in the question that appears in the chunk text is "sulfites", and 201 of the 260 recipes carry the Sulfites caution, one chunk each (201 of 1,823 chunks, about 11%). That is a common word, so it earns a modest weight and a single-word match scores low, and the hits are unrelated recipes that happen to carry it. The vector scores look high (0.746) but the recipes are also unrelated to the question. Questions about a common attribute are not a ranking problem at all; they are a filtering problem.
- **The scores are on different scales.** BM25 reached 15.9 and the vector scores sit between 0.58 and 0.82. You cannot add them without rescaling, which is the job of the pipeline in Task 4.

## When each one wins

This table reflects the probe above plus general retrieval behaviour. Rows marked *general* were not measured in this lab.

| The question contains | Better first choice | Why |
| --- | --- | --- |
| A name, code, domain or other exact identifier | BM25 | The literal token matches; an embedding has no stable meaning for it (Probe 1, first query). |
| A description that avoids the document's own words | kNN | It matches meaning, not words (Probe 1, third and fourth queries). |
| A rare, specific keyword | BM25 | A rare word has a high inverse document frequency, so it dominates the score. |
| A word that many documents carry | Neither; use a filter | BM25 gives it a modest weight and a single-word match scores low; kNN returns unrelated documents (Probe 1, last query). |
| A negation or exclusion ("without", "no") | Neither; use a filter | Both treat the negated word as one more term or concept. |
| Misspellings and different word forms | kNN, with caveats (*general*) | The `text` field is not stemmed, so "sulfite" does not match "sulfites" in BM25. Not tested here. |
| Both an identifier and a description | Hybrid | Each side covers the other's blind spot (Task 4). |

## What "approximate" means in kNN

**Exact kNN** compares the question's vector with every stored vector and keeps the best `k`. The work grows in proportion to the number of documents. **Approximate kNN** (ANN) builds an index structure ahead of time so that it compares against only a small part of the data. This lab's vector indexes use **HNSW** (Hierarchical Navigable Small World) on the Lucene engine: a layered graph in which each vector is linked to nearby vectors, and a query walks the graph toward the closest region. It is fast, and it may occasionally miss a true nearest neighbour.

Two numbers control what you get:

- the pool size, which is the `k` inside the `knn` clause (named `candidate_k` in `build_recipe_vector_query()` in `support/common/opensearch_client.py`): how many neighbours the graph search collects;
- the response `size`: how many of those the query returns.

### Probe 2: how close is approximate to exact on this data

Run this from `scripts/baseline-vector-rag/` after the Task 2 ingest. It scores every vector in the index (one per chunk) with OpenSearch's `knn_score` script and compares that exact top 10 chunks with the approximate top 10, for 20 recipe names used as queries.

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
    body = {"size": k, "_source": ["chunk_id"], "query": {"script_score": {
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
    exact_ids = {h["chunk_id"] for h in exact_knn(vec, k)}
    approx = normalize_hits(recipe_knn_search(client, index, vec, k=k, candidate_k=candidate_k))
    agree += len(exact_ids & {h["chunk_id"] for h in approx})
print(f"approximate (HNSW) results found by exact search: {agree}/{k * len(queries)}")
PY
```

**Expected:** one line, `approximate (HNSW) results found by exact search: 200/200`.

**Interpretation:** at 1,823 vectors the approximate search returned exactly the exact results. That is expected for a graph this small, and it means this lab cannot show you the accuracy cost of ANN. That cost appears with large indexes, where you trade a little recall for much lower latency and tune the pool size to buy some recall back. It was not measured here. For a corpus this size, ANN is a free default, not a decision.

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

unfiltered = recipe_knn_search(client, index, vec, k=25, candidate_k=1823)
print(f"no filter: {len(normalize_hits(unfiltered))} chunk hits")
for candidate_k in (25, 100, 300, 1000, 1823):
    resp = recipe_knn_search(client, index, vec, k=25, candidate_k=candidate_k,
                             exclude={"cautions": ["Sulfites"]})
    print(f"exclude Sulfites, candidate_k={candidate_k:4d}: {len(normalize_hits(resp))} chunk hits")
PY
```

**Expected:** six lines, counted in chunks, not recipes.

```text
no filter: 25 chunk hits
exclude Sulfites, candidate_k=  25: 0 chunk hits
exclude Sulfites, candidate_k= 100: 0 chunk hits
exclude Sulfites, candidate_k= 300: 25 chunk hits
exclude Sulfites, candidate_k=1000: 25 chunk hits
exclude Sulfites, candidate_k=1823: 25 chunk hits
```


**Interpretation:** the filter is applied correctly every time; what changes is how many neighbours were available to filter. The question mentions "sulfites", and 201 of the 260 recipes carry the Sulfites caution (one chunk of each says so in its text, and every chunk carries its recipe's cautions as metadata), so the nearest neighbours of this question are mostly those allergen chunks of sulfite recipes, and none of the nearest 100 chunks survives the exclusion. A user who sees "0 results" would reasonably conclude that no sulfite-free recipe exists, which is false. Three ways around it:

1. **Raise the pool** until it covers enough candidates. Tasks 5 and 6 do this with `ann_pool_for_filtered_search()`, which sets the pool to the number of chunks in the index (here 1,823) so a filter never loses a candidate. It costs more work per query, and at a much larger scale you would not do it.
2. **Filter first, rank second.** Task 5 finds candidates with BM25 and the filters, then ranks only those by vector similarity.
3. **Move the filter inside the `knn` clause**, so the engine applies it while it searches the graph. OpenSearch supports this for some engines; this lab does not use it, and it was not tested here.

For a rule such as "never show an allergen", the first option's failure is silent, which is why a hard rule should not depend on the size of a candidate pool.

## Scores are not comparable

- **BM25 is unbounded and relative.** A score of 8.27 means nothing outside this index; adding documents changes every word's rarity and so every score.
- **The cosine score is bounded.** OpenSearch's `cosinesimil` space reports `(1 + cosine) / 2`, so cosine 0.6 is reported as 0.8.
- **Fusing two lists needs a common scale.** Task 4 rescales each list with *min-max normalization*: the best hit of a list becomes 1.0 and the worst becomes 0.0. This is why Task 4's sample scores include 0.5000: the best BM25 chunk gets `1.0 x 0.5` and, in the same way, the best vector chunk gets `1.0 x 0.5`; a chunk that is in both lists adds the two. A fused score tells you rank within this one result set, not how good the match is. Do not use it as a confidence value or compare it across questions.

## Top-k: how many hits the model sees

`top_k` controls how many recipes reach the prompt. Tasks 2, 4, 5 and 6 default to 5 recipes and Task 3 to 10; each recipe contributes up to 1,800 characters, made of its best-matching chunks. (The search itself asks for `chunk_fetch(top_k)` chunks, six per recipe wanted, so that several chunks of one recipe do not crowd out the others.) Raising `k` is the obvious way to rescue a missed document, so measure what it would cost.

### Probe 4: how deep the missed recipe sits

Run this from `scripts/baseline-vector-rag/` after the Task 2 ingest.

```bash
python - <<'PY'
from src.common.chunking import group_by_recipe
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import create_client, normalize_hits, recipe_knn_search

s = load_settings()
client = create_client(s)
model = EmbeddingModel(s)
question = "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?"
vec = to_list(model.encode([question])[0])
chunks = normalize_hits(recipe_knn_search(
    client, s.opensearch_vector_recipes_baseline_index, vec, k=2000, candidate_k=2000))
recipes = group_by_recipe(chunks)  # best chunk per recipe, ranked
for rank, h in enumerate(recipes, start=1):
    if h["source"] == "thegratefulgirlcooks.com" and h["recipe_name"].lower() == "fish and chips":
        print(f"rank {rank} of {len(recipes)} recipes  score={h['score']:.4f}")
PY
```

**Expected:** `rank 97 of 260 recipes  score=0.7471`. The score is the best chunk's.

**Interpretation:** the recipe Task 2 misses is the 97th nearest. To rescue it by raising `k` you would send 97 recipes (about 19 times the default 5) to the model, mostly irrelevant, and the same trick fails for the next question with a different miss. A larger `k` costs tokens and gives the model more look-alike distractors; it is not a substitute for a retriever that finds the right document. The right `k` is the smallest one at which the answer-bearing recipe is usually present, measured on questions you care about.

## Choosing between them

Start from the failure you must avoid, not from the technique:

1. **Identifier or exact-name questions** need BM25 (or a filter on a keyword field). Vector search cannot be trusted with them.
2. **Open-ended or descriptive questions** need vectors. BM25 only finds them when words happen to overlap.
3. **Both kinds in one product** need hybrid fusion, with the weights treated as a setting to evaluate (next section).
4. **A rule that must always hold** ("exclude this allergen", "only this tier") must be a structured filter applied so that it cannot silently empty or leak the result, not a property of ranking.

## Hybrid fusion and the weights

Task 4's search pipeline computes `0.5 x normalized BM25 + 0.5 x normalized vector` for each chunk. **These numbers were not derived or tuned for this lab.** They are a choice made from a few questions: the lab first used a meaning-first 0.3 / 0.7, and moved to equal weights when chunking made 0.3 too little to keep the named recipe near the top (see [Task 4](../tasks/hybrid-rag-native.md#why-equal-weights)). The probes below show what the weight does on this data so you can judge whether it fits your questions.

Run both from `scripts/hybrid-rag-native/` after the Task 4 ingest. They send each query with an inline search pipeline in the request, so the cluster-wide `hybrid-search-pipeline` is not changed. Probe 5 is the same sweep that [Task 4 runs](../tasks/hybrid-rag-native.md#why-equal-weights); it is repeated here so this page reads on its own.

### Probe 5: sweep the weight on the Fish and Chips pair

```bash
python - <<'PY'
from src.common.chunking import chunk_fetch, group_by_recipe
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import VECTOR_FIELD, create_client, normalize_hits

s = load_settings()
client = create_client(s)
embedder = EmbeddingModel(s)
n = chunk_fetch(5)  # 30 chunks, as ask() requests for the top 5 recipes


def fused_recipes(question, vec, bm25_weight):
    """Run the same hybrid query with an inline pipeline, so the cluster-wide pipeline is untouched."""
    body = {
        "size": n,
        "query": {"hybrid": {"queries": [
            {"match": {"text": question}},
            {"knn": {VECTOR_FIELD: {"vector": vec, "k": n}}},
        ]}},
        "search_pipeline": {"phase_results_processors": [{"normalization-processor": {
            "normalization": {"technique": "min_max"},
            "combination": {"technique": "arithmetic_mean",
                            "parameters": {"weights": [bm25_weight, round(1 - bm25_weight, 1)]}}}}]},
    }
    return group_by_recipe(normalize_hits(client.search(index=s.opensearch_hybrid_index, body=body)))


LABEL = {"thegratefulgirlcooks.com": "A", "womensweeklyfood.com.au": "B"}
for source in LABEL:
    question = f"Does the {source} Fish and Chips recipe contain sulfites?"
    vec = to_list(embedder.encode([question])[0])
    print(f"\nQuestion names recipe {LABEL[source]} ({source})")
    for bm25_weight in (0.0, 0.1, 0.2, 0.3, 0.5, 0.7, 0.9, 1.0):
        place = {}
        for rank, hit in enumerate(fused_recipes(question, vec, bm25_weight), start=1):
            if hit["source"] in LABEL and hit["recipe_name"].lower() == "fish and chips":
                place[LABEL[hit["source"]]] = f"#{rank} ({hit['score']:.3f})"
        print(f"  bm25={bm25_weight:.1f} vector={1 - bm25_weight:.1f}   "
              f"A: {place.get('A', 'absent'):12s} B: {place.get('B', 'absent')}")
PY
```

**Expected:** two tables of eight rows.

```text
Question names recipe A (thegratefulgirlcooks.com)
  bm25=0.0 vector=1.0   A: absent       B: #1 (1.000)
  bm25=0.1 vector=0.9   A: #17 (0.100)  B: #1 (0.942)
  bm25=0.2 vector=0.8   A: #9 (0.200)   B: #1 (0.885)
  bm25=0.3 vector=0.7   A: #5 (0.300)   B: #1 (0.827)
  bm25=0.5 vector=0.5   A: #2 (0.500)   B: #1 (0.711)
  bm25=0.7 vector=0.3   A: #1 (0.700)   B: #2 (0.596)
  bm25=0.9 vector=0.1   A: #1 (0.900)   B: #2 (0.480)
  bm25=1.0 vector=0.0   A: #1 (1.000)   B: #2 (0.484)

Question names recipe B (womensweeklyfood.com.au)
  bm25=0.0 vector=1.0   A: absent       B: #1 (1.000)
  bm25=0.1 vector=0.9   A: #24 (0.032)  B: #1 (1.000)
  bm25=0.2 vector=0.8   A: #18 (0.064)  B: #1 (1.000)
  bm25=0.3 vector=0.7   A: #13 (0.096)  B: #1 (1.000)
  bm25=0.5 vector=0.5   A: #4 (0.160)   B: #1 (1.000)
  bm25=0.7 vector=0.3   A: #2 (0.225)   B: #1 (1.000)
  bm25=0.9 vector=0.1   A: #2 (0.289)   B: #1 (1.000)
  bm25=1.0 vector=0.0   A: #2 (0.321)   B: #1 (1.000)
```

Read it as follows:

- **Recipe A needs a real BM25 vote to be found.** It is absent at 0, 17th at 0.1, 9th at 0.2, 5th at 0.3 and 2nd at 0.5. A chunk that only BM25 finds can score at most the BM25 weight, because it earns nothing from the vector side; at 0.3 that is 0.3, which the allergen chunks of unrelated recipes (the vector side likes them for "contain sulfites?") beat. The sample 0.5000 in Task 4 is the 0.5 row.
- **The weight cannot make A rank first until BM25 outweighs vector.** At 0.5 A is second, and first only from 0.7. That is why Task 4's answer comes from the `source:` label printed with each snippet and not from the rank.
- **Recipe B's question keeps B first at every weight**, because both signals already agree on B. A high BM25 weight pulls the *other* Fish and Chips recipe up (from 24th to 2nd), because it shares the name.
- **A fused score is a weight times a normalized score.** Recipe A's 0.500 at the 0.5 row is its best BM25 chunk (normalized to 1.0) times 0.5, with nothing from the vector list. See Scores above.

### Probe 6: the cost of a high BM25 weight

```bash
python - <<'PY'
from src.common.chunking import chunk_fetch, group_by_recipe
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel, to_list
from src.common.opensearch_client import VECTOR_FIELD, create_client, normalize_hits

s = load_settings()
client = create_client(s)
embedder = EmbeddingModel(s)
n = chunk_fetch(5)


def fused_recipes(question, vec, bm25_weight):
    body = {
        "size": n,
        "query": {"hybrid": {"queries": [
            {"match": {"text": question}},
            {"knn": {VECTOR_FIELD: {"vector": vec, "k": n}}},
        ]}},
        "search_pipeline": {"phase_results_processors": [{"normalization-processor": {
            "normalization": {"technique": "min_max"},
            "combination": {"technique": "arithmetic_mean",
                            "parameters": {"weights": [bm25_weight, round(1 - bm25_weight, 1)]}}}}]},
    }
    return group_by_recipe(normalize_hits(client.search(index=s.opensearch_hybrid_index, body=body)))


question = "sweet baked treat without wheat flour"
vec = to_list(embedder.encode([question])[0])
print(f"QUESTION: {question}")
for bm25_weight in (0.0, 0.3, 0.5, 0.7, 1.0):
    names = [h["recipe_name"][:30] for h in fused_recipes(question, vec, bm25_weight)[:3]]
    print(f"  bm25={bm25_weight:.1f} vector={1 - bm25_weight:.1f}   " + " | ".join(names))
PY
```

**Expected:** five lines, one per weight.

```text
QUESTION: sweet baked treat without wheat flour
  bm25=0.0 vector=1.0   No-Bake Chocolate Chip Cookie  | Healthier Classic Peanut Butte | Whole wheat banana bread with 
  bm25=0.3 vector=0.7   No-Bake Chocolate Chip Cookie  | Whole wheat banana bread with  | Healthier Classic Peanut Butte
  bm25=0.5 vector=0.5   Whole wheat banana bread with  | Chocolate Chip Banana Muffins | No-Bake Chocolate Chip Cookie 
  bm25=0.7 vector=0.3   Chocolate Chip Banana Muffins | Awesome Golden Oven Fried Chic | Whole wheat banana bread with 
  bm25=1.0 vector=0.0   Chocolate Chip Banana Muffins | Awesome Golden Oven Fried Chic | Healthier Classic Peanut Butte
```

**Interpretation:** the weights pull in opposite directions, but on this question the cost starts later than you might expect. Up to 0.5 the top three are sweet baked goods or cookies (at 0.5 the muffins join, which is arguably better than the pure-vector list). From 0.7 a fried chicken recipe is already second, because BM25 matches its words ("baked", "sweet"), and at 1.0 only one of the vector's three picks is left. The identifier question in Probe 5 wants BM25 at 0.7 or more before A is first. Equal weights keep the exact match second and still return sensible recipes for a description; going higher would trade one for the other. Neither the pair nor this one question is a benchmark.

To choose weights for a real product, do not argue from two questions. Collect 20 to 50 questions with the recipes that should answer them, run a sweep like Probe 5 over all of them, and pick the weight (or a different fusion method) by the share of questions whose right recipe is in the top `k`. Re-run the sweep when you change the embedding model, the text you embed, how you chunk, or the corpus, because all of them change the score distributions.

## Tradeoffs and limits

- The probes use one small corpus, one embedding model and a handful of queries. They show *how* the retrievers fail and how the weight behaves, not how often.
- BM25 here uses the default analyzer on `recipe_name` and `text` without stemming or synonyms. Adding either would change the BM25 results above.
- Exact versus approximate agreement (Probe 2) holds for 1,823 vectors only.
- Fusion improves what is *retrieved*; it does not make the model's answer correct, and it does not turn ranking into a rule. For rules, use filters and the governance steps in Tasks 5 to 7.

## Related pages

- [Embedding models](EMBEDDING_MODELS.md): what the vector side actually measures, and the limits behind the probes above.
- [Chunking](CHUNKING.md): what goes into the text that both retrievers index.
- [Infrastructure choices](INFRASTRUCTURE_CHOICES.md): why the lab uses one hosted OpenSearch cluster.
- [Architecture](../architecture.md): the retrieval progression task by task (section 8).
