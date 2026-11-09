# Instaclustr provider contract

The implementation uses the current [OpenSearch Provisioning V2 reference](https://instaclustr.redoc.ly/Current/tag/OpenSearch-Provisioning-V2/) and its embedded OpenAPI `OpenSearchClusterV2` schema, inspected on 2026-10-29. HTTP Basic authentication uses the provisioning username and API key. The CLI resolves those values from the invoking shell.

| Method | Path under `https://api.instaclustr.com` | Expected response |
| --- | --- | --- |
| POST | `/cluster-management/v2/resources/applications/opensearch/clusters/v2/` | 202, with the confirmed cluster `id` |
| GET | `/cluster-management/v2/resources/applications/opensearch/clusters/v2/{clusterId}` | 200, with observed cluster state |
| DELETE | `/cluster-management/v2/resources/applications/opensearch/clusters/v2/{clusterId}` | 204, deletion requested |
| GET | `/cluster-management/v2/data-sources/clusters/v2?search=...` | 200, account envelopes containing cluster summaries |
| GET | `/cluster-management/v2/data-sources/cluster/{clusterId}/network-firewall-rules/v2` | 200, array of cluster/rule envelopes |
| POST | `/cluster-management/v2/resources/network-firewall-rules/v2` | 202 accepted, or 409 when that type/network already exists |

The [Account Cluster List V2 reference](https://instaclustr.redoc.ly/Current/tag/Account-Cluster-List-V2/) documents the search syntax. Recovery searches the recorded generated name and `app:OPENSEARCH`, then reads candidates and requires an exact name match. An empty result never authorizes a second create.

The OpenAPI response schema includes `defaultUsername` and `defaultUserPassword` as sensitive, read-only properties. `Cluster.Credentials()` exposes returned values to the protected store; JSON and formatted cluster output exclude them. The code never substitutes management credentials or invents a default data-plane password. When credentials are absent, callers must require configured application credentials.

Connection extraction prefers `loadBalancerConnectionUrl` and DNS `publicEndpoint` / `privateEndpoint`, then an OpenSearch data, coordinator, or ingest node. It rejects plaintext URLs, user information, paths, and invalid ports. The CLI calls `WaitReady` before reconciling firewall rules: the cluster must be `RUNNING`, any reported operation must be `NO_OPERATION`, reported data-centre statuses must be empty or `RUNNING`, and every reported node must be `RUNNING`. It then reconciles both rules and calls `WaitResponsive`, which waits for a usable connection and a successful authenticated OpenSearch data-plane probe. These are separate gates; see [Connect to an OpenSearch Cluster](https://www.instaclustr.com/support/documentation/opensearch/using-opensearch/connect-to-an-opensearch-cluster/).

Requests have a 30-second HTTP timeout, bounded response bodies, and no redirects. Each polling phase has a separate 45-minute maximum and obeys an earlier caller deadline. `WaitReady` retries HTTP 429 and 5xx responses; terminal provisioning failures, invalid responses, authorization failures, and transport errors stop that phase with the ownership record retained. Create performs exactly one POST. Network uncertainty, malformed accepted responses, and server failures leave an ambiguous-create result; callers preserve the pending journal and reconcile before any new create. A confirmed ID is delivered to the durable callback before polling. DELETE acceptance does not prove deletion; `WaitDeleted` waits for HTTP 404.

The [Firewall Rule V2 reference](https://instaclustr.redoc.ly/Current/tag/Cluster-Network-Firewall-Rule-V2/) was checked on 2026-10-30. For owned provisioned clusters, the CLI lists current rules and submits missing `OPENSEARCH` and `OPENSEARCH_DASHBOARDS` entries with `network: "0.0.0.0/0"`. It uses the same shell-provided management credentials and durable cluster ID. Firewall POSTs are not automatically retried after uncertain outcomes; a subsequent `up` re-reads remote rules. An exact duplicate HTTP 409 is accepted as documented. There is no Dashboard readiness request.

The broad CIDR is the requested lab behavior, not production guidance. The approval prompt discloses it. Existing unrelated rules are not changed or removed, and attached clusters receive no firewall changes. The CLI does not separately manage firewall deletion; `down` retains its existing whole-owned-cluster deletion lifecycle.

`WaitResponsive` uses its own 45-minute timeout, bounded request timeouts, and cancellation. The callback receives the bounded context. Retryable data-plane errors explicitly use `ReadinessPendingError`; permanent configuration/authentication/certificate errors do not. A management `RUNNING` state alone never establishes application reachability. The CLI persists `awaiting-running`, `awaiting-firewall`, and `awaiting-connection` before those phases, and records `ready` only after the data-plane probe succeeds. Tasks reject the pending phases and recheck data-plane health before execution.

The caller owns approval and ownership checks. It must never call DELETE for an attached cluster. Kafka, Cassandra, and ClickHouse are explicit nonfunctional provider placeholders.

Tests use local HTTP fixtures. They make no live management requests, create no billable resources, and establish no live account/region compatibility.
