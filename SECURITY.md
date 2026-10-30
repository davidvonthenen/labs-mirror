← [Instaclustr Labs](README.md) · [Contributing](CONTRIBUTING.md) · [Report a problem](https://github.com/instaclustr/labs/issues/new/choose)

# Security

Instaclustr Labs are teaching material, not a deployed service, so the risks here are mostly about **credentials leaking into the repository** and about learners copying lab shortcuts into production. Both are avoidable.

## Never commit credentials

Courses ship placeholders only:

- Bruno environments (for example `opensearch/01-vector-storage-and-search/bruno/environments/Local.bru`) contain `YOUR-CLUSTER-IP` and `YOUR_PASSWORD`. Fill those in locally and never commit the result.
- Chapter text uses `YOUR_MODEL_ID`, `YOUR_CLUSTER_HOST`, `YOUR_BASE64_VALUE`, and similar. Keep it that way when you edit.
- Redact cluster hostnames, usernames, passwords, and API keys from anything you paste into an issue or a pull request. A base64 authorization header is **encoding, not encryption**: anyone can decode it in one second, so treat it exactly like a password.

## Lab shortcuts that are not production practice

Several things in these courses are fine for a throwaway trial cluster and wrong for a real one. Each is flagged where it appears, and they are collected here by course.

### OpenSearch · [Optimizing Vector Storage & Search](opensearch/01-vector-storage-and-search/README.md)

- **Replicas set to zero.** [Chapter 5](opensearch/01-vector-storage-and-search/chapters/05-production-optimizations/README.md) drops replicas to heal a deliberately broken cluster. In production, add nodes instead; a shard with no replica has no redundancy.
- **A single shared admin user.** The course uses the cluster's default user for everything. In production, create per-application accounts with least privilege.
- **The MCP server and agents inherit the caller's permissions.** If you expose the MCP server built in [Chapter 4](opensearch/01-vector-storage-and-search/chapters/04-rag-optimization/README.md) to an AI client, the client can do whatever that account can do. Scope it to a dedicated read-only service account, and register only the tools that account should have. Never register delete-capable tools for a customer-facing agent.
- **Relaxed ML circuit-breaker thresholds.** [Chapter 2](opensearch/01-vector-storage-and-search/chapters/02-neural-search-pipelines/README.md) raises memory thresholds so a small trial cluster can hold a model. On a production cluster, size the nodes correctly instead of disabling the guardrails that protect them.
- **Firewall open to a single IP.** That is right for a laptop and a trial. Production clusters belong behind private networking, not an allow-listed home IP address.

## Reporting a security issue

If you find a security problem in the course material itself (a committed credential, an instruction that would expose a cluster), please [open an issue](https://github.com/instaclustr/labs/issues/new/choose) if it is not sensitive, or contact the Instaclustr Labs maintainers privately if it is.

Security issues in **OpenSearch** belong upstream with the [OpenSearch project](https://opensearch.org/), and issues in the **Instaclustr platform** belong with [NetApp Instaclustr support](https://www.instaclustr.com/support/).
