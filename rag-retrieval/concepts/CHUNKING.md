# Chunking: why this lab embeds each recipe whole, and when you would not

[Return to Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md)

**Chunking** means splitting a document into smaller pieces before you embed and index it, so that each piece gets its own vector and can be retrieved on its own. This lab does not chunk: each recipe is embedded as one piece. This page explains why that is the right call here, what would change it, and how the common chunking strategies work and when each earns its cost.

You do not need to run anything on this page to complete a task.

## What this concept means

A vector is one point per text. If the text covers several unrelated topics, its vector is a blend of all of them, and a question about one topic matches that blend less sharply than it would match a text about that topic alone. Chunking trades one blurry vector for several sharper ones. The price:

- **More vectors to store and search**, and more documents to rank.
- **Lost context.** A fragment no longer says which document it came from, unless you put that back.
- **Several hits per document.** Retrieved chunks from one document can fill the result list and push out other documents.
- **A new design problem:** where to cut, how much to overlap, and how to cite a piece of a document.

So chunking is a response to a specific problem (texts that are too long, or too varied, for one vector to represent), not a default step. If the problem is absent, chunking costs you the items above and gains you nothing.

## Why this lab does not chunk

**1. The documents are short and about one thing.** Each recipe is rendered by `build_text()` into its name and source, an ingredient list, its allergen cautions and its health labels. Over the 260-recipe sample, that text has a minimum of 234 characters, a median of 449, a 90th percentile of 659 and a maximum of 1,013. [Task 2's length probe](../tasks/baseline-vector-rag.md#step-2-ingest-the-recipe-sample) prints these numbers from your own copy. A recipe's name, ingredients and cautions describe the same dish, so there is no second topic for the vector to blend.

**2. The sample has no long text to split.** The source dataset has fields for ingredients, labels and calories. It has no preparation instructions and no nutrient breakdown in the text that is embedded. There is nothing like a 3,000-word article here.

**3. One document is one vector, one hit and one citation.** Task 6 cites `[R1]`, `[R2]` and so on, and each handle is one recipe. A governance record that says "the answer used recipe X" is simple to audit. With chunks, a handle would mean "the third fragment of recipe X", and every audit and every dedupe step has to account for that.

**4. Fragments lose their identity, and this lab's central failure is about identity.** Task 2 is about two recipes with the same name. A chunk that begins in the middle of an ingredient list does not say whether it belongs to the thegratefulgirlcooks.com recipe or the womensweeklyfood.com.au one, and the question's domain name is the only thing that tells them apart. Splitting would not remove the Fish and Chips failure; it would add a second way to get the wrong recipe.

**5. The indexes already assume a single chunk.** The recipe mappings reserve `chunk_id`, `chunk_index` and `chunk_count`. Tasks 5 and 6 fill them with the recipe's own ID, `0` and `1` (Task 5's `ingest.py` says "always a single chunk here"), so the fields exist for a later chunked version and carry no meaning today.

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

The examples below use a made-up, longer recipe page. They illustrate where each strategy would cut; they are **not run** in this lab, and none of them is implemented in it. The `|` marks a chunk boundary.

```text
TITLE: Slow-Cooked Beef Chili (source: example-kitchen.invalid)
INTRO: A thick weeknight chili that keeps well for days.
INGREDIENTS: 1 kg beef chuck; 2 onions; 3 cloves garlic; 2 tins tomatoes; 2 tsp cumin; 1 tsp smoked paprika.
METHOD: 1. Brown the beef in batches. 2. Soften the onions and garlic. 3. Add tomatoes and spices, then simmer for 3 hours.
NUTRITION (per serving): 410 kcal; 31 g protein; 780 mg sodium.
ALLERGENS: Contains no major allergens. Check stock cubes for sulfites.
NOTES: Freeze for up to three months. Reheat until steaming.
```

### No chunking (this lab)

Embed the whole text as one vector. **Use it when** documents are short, single-topic and under the model's input limit, and when one document should equal one citation. **It fails when** the document is long or mixed: the vector blurs, or the tail is cut off.

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

### Structure-aware splitting

Cut at the document's own sections. In the example, each labelled section becomes a chunk.

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

**Use it with every strategy above that creates fragments.** It is what keeps a fragment attached to its recipe. This lab already does the equivalent for whole documents: `build_text()` begins with the recipe name and source, which is why a question naming a domain has something to match.

### Choosing

| Your documents | Start with |
| --- | --- |
| Short, single-topic, under the model's input limit (this lab) | No chunking |
| Prose of mixed length, no structure | Recursive splitting, with overlap, plus context-enriched chunks |
| Reliable headings, fields or HTML | Structure-aware, plus context-enriched chunks |
| Long documents where the model needs the surroundings | Parent-child |
| Long transcripts with real topic changes | Semantic chunking, if a simpler strategy tested worse on your questions |

Whatever you start with, compare it with the next simplest option on 20 to 50 real questions and compare how often the right document or passage is in the top `k` (see the sweep in [BM25 versus kNN](BM25_VS_KNN.md#hybrid-fusion-and-the-weights-03-and-07)). Chunk size, overlap and strategy are settings you evaluate, not facts you look up.

## Document design: what goes into the embedded text

Without chunking, the main design decision is *what text to embed* and *what to keep in separate fields*. This lab's choice is visible in `build_text()` in each task's `ingest.py`:

| Included in the embedded and searched text | Also stored as a separate keyword field |
| --- | --- |
| Recipe name and source | `recipe_name`, `source`, `url` |
| Ingredients | (none; free text only) |
| Allergen cautions, spelled out in words | `cautions` (for filters) |
| Health labels, spelled out in words | `health_labels`, `diet_labels`, `cuisine_type`, `dish_type` and others |

Two principles sit behind it:

- **Put in the text anything a user's wording might match**, including the facts you also store as fields. The cautions are written into the text so that a question containing "soy" has the literal word to match, and so the embedding can see them, as long as the text fits the model's input limit.
- **Keep the rule-bearing data in a field as well**, so that a rule such as "exclude Sulfites" is enforced by a filter and does not depend on ranking.

Long text also meets limits that fail without an error: the embedding model reads only its first N tokens ([Embedding models](EMBEDDING_MODELS.md#input-limit)), and any cap you put on text before it reaches the LLM drops the rest in the same way. If your documents are long, check both against your longest document.

## Chunking and governance

Whatever you chunk, every piece must stay traceable to its source: keep the document ID, the source and URL, the position of the chunk, and the original text separate from any text you added for embedding. If you add chunking to this lab you will also need to decide how Task 6's `[R#]` handles work (per chunk or per recipe), deduplicate so that one recipe's chunks do not fill the top `k`, and decide how the audit log in Task 6 records which chunk supported an answer. The reserved `chunk_*` fields are a starting point, not a design.

## Tradeoffs and limits

- The lab measured the length of its own text. It did **not** build a chunked version, so it makes no claim about how chunked retrieval would score on the Fish and Chips pair or any other question.
- The strategy examples are illustrations on a made-up recipe page. They are not output from this lab's code.
- Strategy advice here is general retrieval practice. Test it on your own documents and questions before relying on it.

## Related pages

- [Embedding models](EMBEDDING_MODELS.md): the input limit that makes length matter.
- [BM25 versus kNN](BM25_VS_KNN.md): how the retrievers use the text you embed, and how to evaluate a setting.
- [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md): the entity tags stored beside each recipe's text.
- [Choosing a retrieval approach](CHOOSING_RETRIEVAL.md): where chunking and the retrieval approaches fit together.
- [Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md) and [Task 6: Governance Tiering](../tasks/governance-tiering.md): where `build_text()` and the `[R#]` handles are used.
- [Architecture](../architecture.md): the data and ingestion contracts (section 9).
