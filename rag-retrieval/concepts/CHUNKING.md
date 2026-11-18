# Chunking: how this lab splits recipes, and when you would use another strategy

[Return to Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md)

**Chunking** means splitting a document into smaller pieces before you embed and index it, so that each piece gets its own vector and can be retrieved on its own. This lab chunks: each recipe is split by section into 6 to 8 chunks, and every task indexes and searches those chunks. This page explains why, how the split works, what it costs, and how the common chunking strategies compare, including the ones this lab does not use.

You can complete every task without running anything on this page. It has one optional probe, in [What the prefix does](#what-the-prefix-does), which needs no cluster.

## What this concept means

A vector is one point per text. If the text covers several unrelated topics, its vector is a blend of all of them, and a question about one topic matches that blend less sharply than it would match a text about that topic alone. Chunking trades one blurry vector for several sharper ones. The price:

- **More vectors to store and search**, and more documents to rank.
- **Lost context.** A fragment no longer says which document it came from, unless you put that back.
- **Several hits per document.** Retrieved chunks from one document can fill the result list and push out other documents.
- **A new design problem:** where to cut, how much to overlap, and how to cite a piece of a document.

So chunking is a response to a specific problem (texts that are too long, or too varied, for one vector to represent), not a default step. If the problem is absent, chunking costs you the items above and gains you nothing.

## Why this lab chunks

**1. A whole recipe is longer than the embedding model can read.** With the page text added (see [Synthetic page text](../tasks/baseline-vector-rag.md#synthetic-page-text)), every one of the 260 recipes is over the 256-token input limit of `all-MiniLM-L6-v2`: the median is 482 tokens and the longest 758. The model silently ignores everything past the limit, which is most of the method. [Task 2's probe](../tasks/baseline-vector-rag.md#see-the-chunks) prints these numbers from your own copy. After chunking, the longest chunk is 233 tokens.

**2. A question is usually about one part of a recipe.** "Does it contain sulfites?" is about the allergen cautions, and "how long do I fry it?" is about one method step. One vector for the whole recipe is a blend of all of it, so those questions match it less sharply than they match the one chunk that answers them. This is the textbook reason to chunk, and the sections here are chosen to follow the questions people ask.

**3. A fragment still has to say which recipe it belongs to, and this lab's central failure is about identity.** Task 2 is about two recipes with the same name. A chunk that begins in the middle of a method does not say whether it belongs to the thegratefulgirlcooks.com recipe or the womensweeklyfood.com.au one. That is why every chunk starts with the recipe name and source (see [What the prefix does](#what-the-prefix-does)).

**4. Chunking has a cost, and the lab shows it.** The allergen cautions are now a short chunk of their own, so for a question about an allergen, the best-matching chunk of almost *every* recipe is its allergen chunk. In Task 2's vector search the top hits for "Does the thegratefulgirlcooks.com Fish and Chips recipe contain sulfites?" are the allergen chunks of unrelated recipes, and the recipe it names ranks 97th of 260. Chunking makes the allergen text easier to match and makes it harder to tell *which recipe* matched. The fixes in the later tasks (BM25, hybrid fusion, the named-source rule) are about identity, not about chunks.

## How this lab splits a recipe

The strategy is *structure-aware* with *context-enriched* chunks (both described below): a recipe's own sections become chunks, and each chunk starts with the recipe's name and source. [`chunk_recipe()`](../support/common/chunking.py) builds them:

| Chunk | What it holds | Source of the text |
| --- | --- | --- |
| `Ingredients` | The ingredient list. Split into parts if it is over 700 characters, so no chunk is over the token limit. | Original recipe record |
| `Allergen cautions and labels` | The cautions and the health labels. | Original recipe record |
| `Introduction` | A short description of the dish. | Synthetic |
| `Method steps 1-3`, `4-6`, ... | The method, three steps per chunk. | Synthetic |
| `Serving and storage` | Serving and storage notes. | Synthetic |

The 260 recipes become 1,823 chunks (6 to 8 per recipe; median 92 tokens, longest 233). Two rules do the work:

- **Every chunk text starts with `<name> (source: <source>) - <section>: `.** The embedding and the BM25 index both see it, so a question that names a source or a dish has something to match in every chunk.
- **Every chunk carries its recipe's fields.** `recipe_id`, `cautions`, `tier`, `source` and the other filter fields are copied onto each chunk, so a hard filter such as "exclude sulfites" works on chunks exactly as it did on whole recipes.

The sizes (three steps, 700 characters) are choices, not tuned values. This lab has no fixed chunk size and no overlap: chunk length follows each recipe's sections, and the only limits are three method steps per chunk and 700 characters per ingredient part. The ingredient, caution and label text is from the original records, and the introduction, method and notes are synthetic.

### What the prefix does

This probe embeds every chunk twice, once as indexed and once with the prefix removed, and shows where each Fish and Chips recipe ranks among the 260 recipes (best chunk per recipe, by cosine similarity) for the question that names it. It runs in memory, with no cluster and no LLM call, and writes nothing. Run it from `scripts/baseline-vector-rag/` with the `.venv` active.

```bash
python - <<'PY'
import numpy as np

from src.common.chunking import chunk_body, iter_chunks, load_recipes
from src.common.config import RECIPES_SAMPLE_PATH, load_settings
from src.common.embeddings import EmbeddingModel

recipes = load_recipes(RECIPES_SAMPLE_PATH)
model = EmbeddingModel(load_settings()).model
pairs = list(iter_chunks(recipes))
ids = np.array([r["recipe_id"] for r, _ in pairs])
with_prefix = [c["text"] for _, c in pairs]
without = [chunk_body(c) for _, c in pairs]  # the same chunks, minus "<name> (source: ...) - <section>: "
vectors = {
    "with the name/source prefix": model.encode(with_prefix, normalize_embeddings=True, batch_size=64),
    "body text only": model.encode(without, normalize_embeddings=True, batch_size=64),
}

fish = {r["source"]: r["recipe_id"] for r in recipes if r["recipe_name"].lower() == "fish and chips"}
for source in ("thegratefulgirlcooks.com", "womensweeklyfood.com.au"):
    question = f"Does the {source} Fish and Chips recipe contain sulfites?"
    q = model.encode([question], normalize_embeddings=True)[0]
    print(f"\nQuestion names {source}")
    for label, matrix in vectors.items():
        scores = matrix @ q
        best = {rid: scores[ids == rid].max() for rid in set(ids)}  # best chunk per recipe
        order = sorted(best, key=best.get, reverse=True)
        print(f"  {label:28s} rank of the named recipe: {order.index(fish[source]) + 1:3d} of {len(order)}")
PY
```

**Expected:** a model-loading log line and a progress bar on stderr, then:

```text
Question names thegratefulgirlcooks.com
  with the name/source prefix  rank of the named recipe:  97 of 260
  body text only               rank of the named recipe: 200 of 260

Question names womensweeklyfood.com.au
  with the name/source prefix  rank of the named recipe:   1 of 260
  body text only               rank of the named recipe:  77 of 260
```

The prefix is what puts womensweeklyfood.com.au's recipe first and moves thegratefulgirlcooks.com's from 200th to 97th. Without it, a chunk reads like any other fragment of cooking text. The "with" figures are the same ranks that Task 2's probe reads from the live index. 97th is still a miss, which is why the prefix is necessary and not sufficient.

## When you would chunk

Ask these questions about *your* documents:

| Question | If the answer is yes |
| --- | --- |
| Is a typical document longer than the embedding model's input limit? | Chunk, or the tail is ignored (see [Embedding models](EMBEDDING_MODELS.md#input-limit)). |
| Does one document cover several topics people ask about separately? | Chunk along the topic boundaries, so each vector is about one thing. |
| Is the answer usually a small passage inside a large document? | Chunk, and return the passage with a link back to the whole. |
| Must the model see an entire document to answer (a contract clause in context, a full recipe method)? | Do not shrink what you hand to the model; use parent-child retrieval (below). |
| Do users need a citation to the exact passage? | Chunk, and store each chunk's position with it. |

Dataset *size* is not on the list. Forty thousand short documents need chunking no more than forty do. What matters is the length and variety of each individual document.

## Strategies and when to apply each

The examples below use a made-up, longer recipe page. They illustrate where each strategy would cut; they are **not output from this lab's code**. Only structure-aware splitting with context-enriched chunks (described below) is implemented in the lab. The `|` marks a chunk boundary.

```text
TITLE: Slow-Cooked Beef Chili (source: example-kitchen.invalid)
INTRO: A thick weeknight chili that keeps well for days.
INGREDIENTS: 1 kg beef chuck; 2 onions; 3 cloves garlic; 2 tins tomatoes; 2 tsp cumin; 1 tsp smoked paprika.
METHOD: 1. Brown the beef in batches. 2. Soften the onions and garlic. 3. Add tomatoes and spices, then simmer for 3 hours.
NUTRITION (per serving): 410 kcal; 31 g protein; 780 mg sodium.
ALLERGENS: Contains no major allergens. Check stock cubes for sulfites.
NOTES: Freeze for up to three months. Reheat until steaming.
```

### No chunking

Embed the whole text as one vector. **Use it when** documents are short, single-topic and under the model's input limit, and when one document should equal one citation. **It fails when** the document is long or mixed: the vector blurs, or the tail is cut off. This lab embedded each recipe whole when a recipe was only its name, ingredients and cautions, which fits easily in the limit; it stopped when the page text was added.

### Fixed size with overlap

Cut every N characters (or tokens), starting each piece a little before the previous one ended.

```text
| TITLE: Slow-Cooked Beef Chili (source: example-kitchen.invalid) INTRO: A thick weeknight chili that keeps well for days. INGREDIENTS: 1 kg beef chuck; 2 onions; 3 clo |
| ves garlic; 2 tins tomatoes; 2 tsp cumin; 1 tsp smoked paprika. METHOD: 1. Brown the beef in batches. 2. Soften the onions and garlic. 3. Add tomatoes and spices, th |
| ...
```

**Use it when** you have long, uniform text with no reliable structure (transcripts, scraped pages) and want a simple, predictable baseline. The overlap keeps a sentence cut at a boundary from vanishing from both halves. **It fails when** it cuts through the middle of a word, an ingredient or a fact ("3 clo | ves garlic"), and when the piece no longer says which document it belongs to.

### Sentence or paragraph boundaries

Cut where sentences or paragraphs end, and pack neighbouring sentences up to a size limit.

```text
| TITLE: Slow-Cooked Beef Chili (source: example-kitchen.invalid)  INTRO: A thick weeknight chili that keeps well for days. |
| INGREDIENTS: 1 kg beef chuck; 2 onions; 3 cloves garlic; 2 tins tomatoes; 2 tsp cumin; 1 tsp smoked paprika. |
| METHOD: 1. Brown the beef in batches. 2. Soften the onions and garlic. 3. Add tomatoes and spices, then simmer for 3 hours. |
```

**Use it when** the text is prose and each sentence or paragraph is meaningful on its own. **It fails when** a sentence depends on the previous one ("This is why you should not..."), or when a list item is not a sentence, so there is nothing to split on.

### Recursive splitting

Try the coarsest separator first (a blank line), and split a piece further (line, sentence, word) only if it is still too big.

**Use it when** you have mixed documents and want most of the benefit of the two strategies above with one rule: paragraphs stay together unless they have to be broken. It is a sensible general default for unstructured text. **It fails when** the structure that matters is not made of blank lines, such as a table.

### Structure-aware splitting (this lab)

Cut at the document's own sections. In the example, each labelled section becomes a chunk. This lab does this with the sections listed above. Chunks are as long as the sections are, so there is no fixed chunk size. A size limit is only a safeguard for a section that is too long.

```text
| INGREDIENTS: 1 kg beef chuck; 2 onions; ... |   -> retrieved for "what do I need?"
| METHOD: 1. Brown the beef ... simmer for 3 hours. |   -> retrieved for "how long does it cook?"
| NUTRITION (per serving): 410 kcal; 31 g protein; 780 mg sodium. |   -> retrieved for "how much sodium?"
| ALLERGENS: Contains no major allergens. Check stock cubes for sulfites. |
```

**Use it when** documents have reliable headings, fields or HTML structure (manuals, policies, product pages, recipes with real instructions and nutrition text). This is the strategy that matches the "a question about sodium should match the sodium text, not the whole page" argument. **It fails when** sections are very uneven (one huge section) or the structure is inconsistent between documents. Each chunk must also carry the document title, or it is anonymous.

### Parent-child (small to big)

Embed small pieces for precise matching, but when one matches, hand the model its larger parent (the whole section or document).

```text
child (embedded):  | 410 kcal; 31 g protein; 780 mg sodium. |
parent (returned): | the whole NUTRITION section, or the whole recipe page |
```

**Use it when** you need sharp matching but the model needs the surrounding context to answer correctly: legal clauses, procedures with caveats, a recipe method whose steps depend on each other. **It costs** a second lookup (child to parent), a way to store the link, and a rule for what to do when several children of one parent match.

### Semantic chunking

Cut where the topic changes, usually detected by comparing the embeddings of neighbouring sentences.

**Use it when** you have long, unstructured text with no headings, where topic shifts are real and matter (a transcript, a long email thread). **It costs** an embedding pass over every sentence just to find the cuts, and an extra threshold to tune. It can also produce very uneven pieces.

### Context-enriched chunks

Not a way of cutting, but of describing: prepend identifying text (document title, source, section, date) to each chunk before embedding, and keep the original chunk separately as the evidence you show the model.

```text
embedded text:  "Slow-Cooked Beef Chili (example-kitchen.invalid) - NUTRITION: 410 kcal; 31 g protein; 780 mg sodium."
stored text:    "NUTRITION: 410 kcal; 31 g protein; 780 mg sodium."
```

**Use it with every strategy above that creates fragments.** It is what keeps a fragment attached to its recipe, and this lab does it (see [What the prefix does](#what-the-prefix-does)). One simplification: the lab stores a single `text` field that includes the prefix, so the prefix is searched and embedded, and strips it with `chunk_body()` when it shows a chunk to the model. A production design might keep the original chunk and the enriched text in two fields.

### Choosing

| Your documents | Start with |
| --- | --- |
| Short, single-topic, under the model's input limit | No chunking |
| Prose of mixed length, no structure | Recursive splitting, with overlap, plus context-enriched chunks |
| Reliable headings, fields or HTML (this lab) | Structure-aware, plus context-enriched chunks |
| Long documents where the model needs the surroundings | Parent-child |
| Long transcripts with real topic changes | Semantic chunking, if a simpler strategy tested worse on your questions |

Whatever you start with, compare it with the next simplest option on 20 to 50 real questions and compare how often the right document or passage is in the top `k` (see the sweep in [BM25 versus kNN](BM25_VS_KNN.md#hybrid-fusion-and-the-weights)). Chunk size, overlap and strategy are settings you evaluate, not facts you look up.

## Document design: what goes into the embedded text

With chunking, the design decisions are *how to cut* and *what text to embed in each chunk*, plus *what to keep in separate fields*. This lab's choice is visible in `chunk_recipe()`:

| Included in the embedded and searched text | Also stored as a separate keyword field |
| --- | --- |
| Recipe name and source (the prefix of every chunk) | `recipe_name`, `source`, `url` |
| Ingredients | (none; free text only) |
| Allergen cautions, spelled out in words | `cautions` (for filters) |
| Health labels, spelled out in words | `health_labels`, `diet_labels`, `cuisine_type`, `dish_type` and others |

`build_text()` still exists, but it is no longer what gets embedded: it renders a recipe's facts (name, source, ingredients, cautions, labels), and the BM25 tasks send that text to the NER service once per recipe, so a recipe's `entities` come from its facts and are copied onto all of its chunks.

Two principles sit behind the design:

- **Put in the text anything a user's wording might match**, including the facts you also store as fields. The cautions are written into a chunk of their own so that a question containing "soy" has the literal word to match, and so the embedding can see them.
- **Keep the rule-bearing data in a field as well**, so that a rule such as "exclude Sulfites" is enforced by a filter and does not depend on ranking.

Long text also meets limits that fail without an error: the embedding model reads only its first N tokens ([Embedding models](EMBEDDING_MODELS.md#input-limit)), and any cap you put on text before it reaches the LLM drops the rest in the same way. This lab caps the evidence for each recipe at 1,800 characters, made of its best-matching chunks (at most three).

## Chunking and governance

Whatever you chunk, every piece must stay traceable to its source: keep the document ID, the source and URL, the position of the chunk, and the original text separate from any text you added for embedding. This lab makes these choices:

- **Citation handles name recipes, not chunks.** Task 6's `[R1]`, `[R2]` and so on each stand for one recipe, whose best-matching chunks (up to three) are shown to the model as its evidence. Handles stay simple, and "the answer used recipe X" is still easy to audit.
- **One recipe's chunks cannot fill the result list.** `group_by_recipe()` folds ranked chunks into recipes, keeping the best chunk score, before anything is counted or shown. Searches ask for `chunk_fetch(n)` chunks (six per recipe wanted) so that the folded list still has `n` recipes.
- **The audit record names the chunks.** Task 6's `evidence_chunk_ids` lists the chunks that were shown to the model, so a past answer can be traced to exact passages, and `pinned_named_sources` records when the named-source rule added a recipe.
- **The `chunk_id`, `chunk_index`, `chunk_count` and `section` fields are filled** on every chunk (the chunk ID is the recipe ID plus the position), and `chunk_id` is the document ID in the vector indexes.

## Tradeoffs and limits

- The lab chunked one corpus with one strategy. The chunk sizes (three steps per chunk, 700 characters of ingredients) are plausible settings, not tuned ones, and the lab did not compare chunk strategies against each other.
- Whole-recipe retrieval was measured only on a five-recipe proof of concept before the lab switched, and the ranks above come from the full chunked index. The lab makes no claim that chunking improves retrieval in general: it was needed to fit the model's input limit, and it did not fix the Fish and Chips miss for vector search.
- The page text (introduction, method, serving notes) is synthetic, written by an LLM from the ingredient list. It exercises the chunking code. It is not real recipe text, and results on real recipe pages may differ.
- The strategy examples are illustrations on a made-up recipe page. They are not output from this lab's code.
- Strategy advice here is general retrieval practice. Test it on your own documents and questions before relying on it.

## Related pages

- [Embedding models](EMBEDDING_MODELS.md): the input limit that makes length matter.
- [BM25 versus kNN](BM25_VS_KNN.md): how the retrievers use the text you embed, and how to evaluate a setting.
- [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md): the entity tags stored beside each recipe's text.
- [Choosing a retrieval approach](CHOOSING_RETRIEVAL.md): where chunking and the retrieval approaches fit together.
- [Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md) and [Task 6: Governance Tiering](../tasks/governance-tiering.md): where `chunk_recipe()` and the `[R#]` handles are used.
- [Architecture](../architecture.md): the data and ingestion contracts (section 9).
