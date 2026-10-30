# Embedding models, and why this lab uses MiniLM

[Return to Task 2: Baseline Vector RAG](../tasks/baseline-vector-rag.md) · [Task 4: Hybrid RAG (Native)](../tasks/hybrid-rag-native.md)

Every vector search in this lab, from Task 2 onward, depends on one choice made once in `.env`: which embedding model turns text into numbers. This page explains what that model captures and misses, which settings it imposes on your index and your text, which other models exist and when you would pick them, and why the lab uses a small one.

## What this concept means

An **embedding model** maps a piece of text to a fixed-length list of numbers (a *vector*) so that texts with similar meaning land near each other. Retrieval then reduces to geometry: embed the question, find the stored vectors nearest to it.

Three consequences shape every decision below:

1. **Question and documents must use the same model.** Vectors from different models live in different spaces, even when they have the same length. Comparing them is meaningless.
2. **The model decides what "similar" means.** It was trained on some data for some purpose, and it is similar *in that sense*. Task 2 is built on the gap between that sense and what the question needs.
3. **Changing the model changes the index.** Stored vectors cannot be converted; every document has to be embedded again.

In this lab the model is `sentence-transformers/all-MiniLM-L6-v2`, wrapped in `EmbeddingModel` in `support/common/embeddings.py`. It outputs 384 numbers per text, normalised to length 1, and runs on a laptop CPU.

## Probe 1: what MiniLM captures and what it misses

Run this from `scripts/baseline-vector-rag/` with the `.venv` active. It embeds six sentence pairs locally. It does not need OpenSearch, makes no LLM call and writes nothing.

```bash
python - <<'PY'
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel

model = EmbeddingModel(load_settings())

pairs = [
    ("synonym",            "I want a vegetarian dinner",              "meatless evening meal idea"),
    ("negation",           "This recipe contains sulfites",           "This recipe contains no sulfites"),
    ("negation (control)", "This recipe contains sulfites",           "This recipe is made with sulfites"),
    ("domain token",       "thegratefulgirlcooks.com Fish and Chips", "womensweeklyfood.com.au Fish and Chips"),
    ("domain (control)",   "thegratefulgirlcooks.com Fish and Chips", "Chocolate brownies with walnuts"),
    ("unrelated",          "This recipe contains sulfites",           "How do I reset my router?"),
]
for label, a, b in pairs:
    va, vb = model.encode([a])[0], model.encode([b])[0]
    print(f"{label:20s} cosine={float(va @ vb):.3f}   {a!r} vs {b!r}")
PY
```

**Expected:** a model-loading log line and progress bar on stderr, then six lines. The exact digits can differ in the last place across library versions; the order should not.

```text
synonym              cosine=0.604   'I want a vegetarian dinner' vs 'meatless evening meal idea'
negation             cosine=0.943   'This recipe contains sulfites' vs 'This recipe contains no sulfites'
negation (control)   cosine=0.962   'This recipe contains sulfites' vs 'This recipe is made with sulfites'
domain token         cosine=0.799   'thegratefulgirlcooks.com Fish and Chips' vs 'womensweeklyfood.com.au Fish and Chips'
domain (control)     cosine=0.149   'thegratefulgirlcooks.com Fish and Chips' vs 'Chocolate brownies with walnuts'
unrelated            cosine=0.041   'This recipe contains sulfites' vs 'How do I reset my router?'
```

What each line tells you:

- **Synonyms are captured.** "Vegetarian dinner" and "meatless evening meal idea" share no content word and still score 0.604, far above the unrelated pair (0.041). This is what BM25 cannot do.
- **Negation is mostly not captured.** "Contains sulfites" and "contains *no* sulfites" score 0.943, only slightly below a sentence that agrees with the first one (0.962). For an allergen question, the two opposite statements are nearly the same point in space.
- **Unusual identifiers are blurred.** Two different domains with the same dish name score 0.799, so changing the domain moves the vector only a little, while an unrelated dish scores 0.149. This is the Task 2 failure in miniature: the question names one domain, and the recipe with the other domain is almost as close.
- **Cosine values have no universal scale.** 0.6 is high for this model's synonym pair and would be unremarkable for a different model. Never carry a threshold from one model to another.

## The settings a model imposes

### Dimension and memory

Every vector has one number per dimension, stored as a 32-bit float (4 bytes) in OpenSearch. The index mapping must declare the same dimension as the model. This lab takes it from the loaded model (`embedder.dimension`) when it creates an index; `EMBEDDING_DIMENSION` in `.env` is only a fallback. The probe below prints the numbers for this model.

#### Probe 2: the numbers for this model

Run it from `scripts/baseline-vector-rag/` with the `.venv` active. It needs no OpenSearch, makes no LLM call and writes nothing.

```bash
python - <<'PY'
import numpy as np
from src.common.config import load_settings
from src.common.embeddings import EmbeddingModel

model = EmbeddingModel(load_settings())
a, b = model.encode(["Fish and Chips", "Fish and Chips with tartare sauce"])

print(f"model={model.model_name}  dimension={model.dimension}  input limit={model.model.max_seq_length} tokens")
print(f"norm={np.linalg.norm(a):.4f}  (L2-normalized, so dot product == cosine)")
per_vector = model.dimension * 4  # float32
print(f"raw float32 size: {per_vector} bytes per vector; {per_vector * 1_000_000 / 1e9:.2f} GB per million vectors")
cos = float(a @ b)
l2sq = float(np.sum((a - b) ** 2))
print(f"cosine={cos:.4f}  dot={float(np.dot(a, b)):.4f}  squared L2={l2sq:.4f}  (2 - 2*cosine = {2 - 2 * cos:.4f})")
PY
```

**Expected:** four lines.

```text
model=sentence-transformers/all-MiniLM-L6-v2  dimension=384  input limit=256 tokens
norm=1.0000  (L2-normalized, so dot product == cosine)
raw float32 size: 1536 bytes per vector; 1.54 GB per million vectors
cosine=0.7157  dot=0.7157  squared L2=0.5686  (2 - 2*cosine = 0.5686)
```

The 1.54 GB is the raw vectors only. The HNSW graph, the document text and replicas add to it, and a 1,024-dimension model needs about 2.7 times as much (1,024 / 384). Dimension is therefore a cost decision as well as a quality one. The sample here has 260 recipes (about 0.4 MB of vectors), so cost does not matter in this lab, and it would at a million documents.

### Distance: cosine, dot product and L2

Because `EmbeddingModel.encode()` normalises every vector to length 1, three measures give the same ranking: cosine similarity, dot product, and squared Euclidean distance (the last line above shows `squared L2 = 2 - 2 x cosine`). That is why Task 2 can use a `cosinesimil` index and Task 4's `recipes-hybrid` index can use `l2`: the order of results is the same, only the reported score differs. If you switch to a model that does not normalise its output, the three measures stop agreeing, and you must use the metric the model was trained for.

### Input limit

Each model reads a fixed number of *tokens* (word pieces), and silently ignores the rest. MiniLM's limit is 256 tokens, a figure its model card states: "input text longer than 256 word pieces is truncated". The lab embeds a rendered recipe (name, source, ingredients, cautions, health labels). Most recipes are far below the limit, but five of the 260 are not, and the longest is 307 tokens. [Task 2's length check](../tasks/baseline-vector-rag.md#check-the-recipe-lengths) prints these numbers from your own copy. Words past the limit add nothing to the vector, but the full text is still stored in OpenSearch, so BM25 and filters can use it. The lab does not change the layout; [Chunking](CHUNKING.md#document-design-what-goes-into-the-embedded-text) covers what to put in the embedded text and when to split a document.

### Prefixes

Some models are trained to see a marker in front of the text, and perform worse without it. MiniLM needs none. Others do, and the marker usually differs between questions and documents:

| Model | What its card says |
| --- | --- |
| `intfloat/e5-base-v2`, `intfloat/multilingual-e5-base` | Each text should start with `query: ` or `passage: `. |
| `BAAI/bge-base-en-v1.5` (and the small and large v1.5 variants) | For retrieval, add `Represent this sentence for searching relevant passages: ` to the question only, never to passages. |
| `nomic-ai/nomic-embed-text-v1.5` | Embed documents as `search_document: <text>` and questions as `search_query: <text>`. |

`EmbeddingModel.encode()` in this lab adds no prefix. Switching to one of these models means adding it in your own code, in the right place for questions and for documents. Forgetting is easy and silent: nothing fails, and retrieval is just worse.

## Model families and when to use each

The first model in the table is the one this lab runs. Every other row is **not run here**: its dimension and input limit come from the files in its public repository (`config.json` and `sentence_bert_config.json`, or the provider's documentation), read on 2026-10-10, and "when to consider" is a judgement, not a measurement. Check the model card for licence and current details before you adopt one.

| Model | Dimension | Input limit (tokens) | When to consider |
| --- | ---: | ---: | --- |
| `all-MiniLM-L6-v2` (**this lab**) | 384 | 256 | A small, general-purpose model for teaching, prototypes and CPU-only machines. About 91 MB of weights, Apache-2.0 licence. |
| `all-mpnet-base-v2` | 768 | 384 | The next step up from MiniLM in the same family, for more quality at about twice the vector size, still local. |
| `BAAI/bge-small-en-v1.5` | 384 | 512 | A small English model with a longer input limit than MiniLM. Same vector size, so the same memory. |
| `BAAI/bge-base-en-v1.5` / `bge-large-en-v1.5` | 768 / 1024 | 512 | English retrieval where quality matters more than size and latency. Uses the query instruction above. |
| `intfloat/e5-base-v2` | 768 | 512 | English retrieval that uses separate query and passage prefixes. Requires `query: ` and `passage: `. |
| `intfloat/multilingual-e5-base` | 768 | 512 | Questions and documents in several languages. Same prefixes. |
| `paraphrase-multilingual-MiniLM-L12-v2` | 384 | 128 | Multilingual sentence similarity on small hardware. Its 128-token limit is half of MiniLM's, so it would truncate far more of this lab's recipes. |
| `nomic-ai/nomic-embed-text-v1.5` | 768 | 8,192 | Long documents without chunking, locally hosted. Requires `search_document: ` and `search_query: `. |
| `BAAI/bge-m3` | 1,024 | 8,192 | Many languages (its card says more than 100) and long inputs, at a large vector size. |
| Amazon Titan Text Embeddings V2 (`amazon.titan-embed-text-v2:0`, Bedrock) | 1,024 (or 512, 256) | 8,192 | You already use Bedrock for generation and want no model to host. Text leaves your machine for AWS. |
| OpenAI `text-embedding-3-small` | 1,536 (reducible with the `dimensions` parameter) | 8,192 | A hosted model with a cost per token and no local hardware. Text leaves your machine for the provider. |

Two columns decide more than the benchmark scores people quote:

- **Input limit against your document length.** A model whose limit is shorter than your documents silently drops the rest (see [Input limit](#input-limit)). A model with a long limit lets you embed whole documents but does not remove the dilution problem described in [Chunking](CHUNKING.md).
- **Dimension against your corpus size.** Vector memory grows with dimension, and so does search latency.

### How to choose

Walk the questions in this order, because the first ones eliminate most candidates:

1. **Language.** Are questions or documents in languages other than English? If yes, start from a multilingual model; an English-only model degrades.
2. **Privacy and residency.** May the text leave your network? If not, only a local or self-hosted model qualifies, whatever the quality.
3. **Text length.** What is the longest thing you need to embed in one piece? Compare it with the input limit.
4. **Hardware and latency.** CPU only, one GPU, or a managed API? Embedding runs at ingest time (once per document) and at query time (once per question), so query latency is the number users feel.
5. **Cost.** Local models cost hardware; hosted ones cost per token, on every ingest and every query, and again for every full re-embedding.
6. **Domain vocabulary.** If your text is full of terms a general model has not seen (clinical codes, part numbers, legal citations), a general model will blur them, as it blurred the domain names in Probe 1. The usual remedies are a domain-tuned or fine-tuned model, or BM25 alongside the vectors. A bigger general model may help less; measure before assuming.
7. **Evidence from your own data.** Build 20 to 50 representative questions with the documents that should answer them, then measure how often the right document is in the top `k` for two or three candidates. A public leaderboard score is not a substitute for that.

## Why this lab uses MiniLM

- **Every task has to run on a laptop.** The model is about 91 MB, runs on CPU, loads in seconds, and needs no key, no account and no per-query cost.
- **It is enough to show the failures.** The lesson of Task 2 is a retrieval failure, and the failure appears with this model: the question about recipe A scores 0.653 against recipe B and 0.519 against A itself. A reader reproduces it without any setup beyond `pip install`.
- **384 dimensions keep the indexes small** and the shared cluster light.

What this choice does *not* show: **whether a stronger model would avoid the Fish and Chips failure.** It was not tested. A larger model might separate the two recipes better, or it might not, because the two recipes' texts differ in little more than a domain name, and Probe 1 shows that kind of difference is only partly visible in an embedding. Treat "use a better model" as a hypothesis to measure with the sweep in [BM25 versus kNN](BM25_VS_KNN.md#hybrid-fusion-and-the-weights-03-and-07), not as a fix.

## Switching the embedding model safely

Do this on a copy of your indexes, not in place, especially on a shared cluster. Steps 3 to 5 follow from reading the code and were not run end to end.

1. **Pick the model** using the questions above, and read its card for the prefix rules, the input limit and whether its output is normalised.
2. **Set `EMBEDDING_MODEL`** in `.env`. Leave `EMBEDDING_DIMENSION` equal to the new model's dimension as well, since it is the fallback if the model cannot report one.
3. **Use new index names.** Each `ingest.py` creates an index with the model's dimension only if the index does not exist. An existing 384-dimension index will reject 768-dimension vectors. Set the `OPENSEARCH_*_INDEX` variables to new names (this lab's `-rrtest` suffix is an example), or delete the old index when it is yours to delete.
4. **Re-ingest every document** with the new model, and make sure questions use the same model, which they do, since both read `EMBEDDING_MODEL`. Never mix old and new vectors in one index.
5. **Add any prefix** the model needs, in the place where documents are embedded and in the place where questions are embedded.
6. **Re-run the checks that depend on the model.** Task 2's `validate.py` is written to *fail* if the Fish and Chips failure stops reproducing, and with a stronger model it may; that is a result, not a bug. Re-run the weight sweep from [BM25 versus kNN](BM25_VS_KNN.md), because fusion weights depend on the score distributions of both retrievers and these change with the model.
7. **Record the model name and version** next to the index, so that you know which vectors you are looking at later.

## Tradeoffs and limits

- Probe 1 uses six short sentences. It shows how this one model treats synonyms, negation and domain names; it does not rank models.
- The table lists published sizes and limits, not quality. No model other than MiniLM was run in this lab.
- A good embedding is not evidence. Task 2's lesson holds for any model: a high similarity says the texts are alike, not that the document answers the question or that its facts match the question's.

## Related pages

- [BM25 versus kNN](BM25_VS_KNN.md): how vector search compares with keyword search, and how to fuse them.
- [Chunking](CHUNKING.md): what to put in the text that gets embedded, and why this lab embeds each recipe whole.
- [Infrastructure choices](INFRASTRUCTURE_CHOICES.md): the other infrastructure decisions behind the lab.
- [Architecture](../architecture.md): where embeddings sit in each task's pipeline.
