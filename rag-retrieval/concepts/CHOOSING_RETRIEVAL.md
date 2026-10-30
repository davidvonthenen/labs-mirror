# Choosing a retrieval approach

[Return to the task index](../README.md#tasks) · [Task 2](../tasks/baseline-vector-rag.md) · [Task 3](../tasks/baseline-bm25-rag.md) · [Task 4](../tasks/hybrid-rag-native.md) · [Task 5](../tasks/hybrid-rag-ner-rerank.md) · [Task 6](../tasks/governance-tiering.md)

The tasks of this lab are a sequence, and each step fixes a failure of the one before. That order is a teaching order, not a ranking. A real system should add a step only when it needs what the step provides, because every step costs something. This page puts the five approaches side by side so you can pick the simplest one that fits.

## What this concept means

All five approaches answer the same first question: *which stored recipes should the model see?* They differ in what they guarantee.

- **Ranking** methods (vector, BM25, hybrid) order recipes by a score. A score is a *preference*: the right recipe is likely to be near the top, and the wrong one is likely to be lower. Nothing in a score is a promise.
- **Rules** (filters) are true or false. A recipe that fails the rule is not in the result set, whatever its score.
- **Governance** (tiers, citations, audit records) does not change what is retrieved by relevance. It makes the result reachable under a policy and explainable after the fact.

## The decision table

| If you need | Use | Task | What it adds | What it still cannot do |
| --- | --- | --- | --- | --- |
| Questions that name exact things: a recipe name, a source, an ingredient word | **BM25** keyword search | [3](../tasks/baseline-bm25-rag.md) | A text index. No model. You can explain a hit by the words it matched | Find a recipe the question only describes. See [BM25 versus kNN, Probe 1](BM25_VS_KNN.md#probe-1-put-the-same-queries-through-both-retrievers) |
| Questions phrased differently from the stored text | **Vector (kNN)** search | [2](../tasks/baseline-vector-rag.md) | An embedding model to load and run, a vector field, and ingest time to embed every recipe | Guarantee that an exact name match is in the results. See [Probe 4](BM25_VS_KNN.md#probe-4-how-deep-the-missed-recipe-sits) |
| Both kinds of question in one system | **Hybrid** BM25 + kNN | [4](../tasks/hybrid-rag-native.md) | Everything above, plus a search pipeline on the cluster and a pair of weights to tune | Remove the trade-off between the two. See [the weights](BM25_VS_KNN.md#hybrid-fusion-and-the-weights-03-and-07) |
| Results that must never include something, such as an allergen | **Filters** in a two-phase design (ground, then refine) | [5](../tasks/hybrid-rag-ner-rerank.md) | Two queries to orchestrate in client code, fields to store the filter values in, and (optionally) the [NER service](NER_IN_RETRIEVAL.md) for the entity boost | Filter on something you did not store. Keep recall when the vector step's neighbour pool is small: the filter is applied after the neighbours are taken (4 of 25 candidates survived at `candidate-k` 25 in the Task 5 check) |
| Different criticality of data, and answers you can trace after the fact | **Governance**: tiering, citation tags, audit log | [6](../tasks/governance-tiering.md), [7](../tasks/operationalize-production.md) | A tier policy, an audit index, mandatory citations, and a monitor and retention policy to run | Enforce anything by itself: citations are checked and recorded, not blocked. A tier is a label on a recipe, not a different kind of storage |

## How to choose in practice

Ask these in order, and stop at the first step that satisfies your requirement.

1. **Do users type names and identifiers?** Start with BM25. It is the cheapest to run and the easiest to explain.
2. **Do users describe what they want in their own words?** Add vector search. Measure on your own questions before you trust it. In this lab's data, vector search missed a recipe whose name matched the question word for word.
3. **Do you get both kinds of question?** Use hybrid. Plan to choose the weights from a set of your own questions, not from this lab's two examples.
4. **Is there a result that would be harmful or wrong to return?** Do not rely on any score for it. Store the property as a field and filter on it, as in Task 5. Check the filter's recall as well as its precision.
5. **Will someone ask why an answer was given, or does some data matter more than the rest?** Add the governance layer. Decide the response policy for a failed check *before* you add the check, because Task 6 records failures and does not act on them.

Two further points:

- **Entities are optional at every step.** The NER service of Tasks 3, 5 and 6 adds a source-selecting boost and a second process to run. Task 4 retrieves the same Fish and Chips pair without it. See [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md) for when it is worth having.
- **Each step you add is a thing to keep running.** The embedding model, the search pipeline, the NER service and the audit index each fail in their own way. [Infrastructure choices](INFRASTRUCTURE_CHOICES.md) lists what runs where.

## Tradeoffs and limits

- This page summarises what the lab's probes showed on 260 recipes, one embedding model and a handful of questions. It is not a benchmark, and the order of steps is not a claim about which approach is most accurate on your data.
- The costs in the table are qualitative. The lab did not measure latency, memory under load or hosting cost for any step.
- Real systems often combine steps in other ways, for example vector search with filters and no BM25. The table lists what each step provides, not the only valid combinations.

## Related pages

- [BM25 versus kNN](BM25_VS_KNN.md): the first three rows, with the probes behind them.
- [Named-entity recognition in retrieval](NER_IN_RETRIEVAL.md): what the optional entity boost does.
- [Embedding models](EMBEDDING_MODELS.md) and [Chunking](CHUNKING.md): the choices behind the vector side.
- [Infrastructure choices](INFRASTRUCTURE_CHOICES.md): what runs where.
- [Architecture](../architecture.md): the retrieval progression task by task.
