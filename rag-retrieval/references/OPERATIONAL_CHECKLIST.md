# Operational Checklist

Production-hardening checklist for this project, built out in
[Operationalize Production](../tasks/operationalize-production.md). Each item below is
marked **automated** (a script in `scripts/operationalize-production/src/` you can
actually run, with a `validate.py` smoke test) or **manual** (requires
cluster-admin/AWS-console access this codebase's app credentials
intentionally don't hold, so it stays a documented checklist item).

## A. Infrastructure & access control

- [ ] **Manual.** Scope IAM to the specific Bedrock model ARN(s) in use.
  See [`scripts/operationalize-production/src/hardening/bedrock_invoke_policy.json`](../scripts/operationalize-production/src/hardening/bedrock_invoke_policy.json).
- [ ] **Manual.** Role-based OpenSearch security instead of one shared
  cluster credential. See [`scripts/operationalize-production/src/hardening/opensearch_roles.json`](../scripts/operationalize-production/src/hardening/opensearch_roles.json).
- [ ] **Manual.** Verify Instaclustr backup/PITR is configured *and*
  actually restorable.
- [ ] **Manual, not yet applicable.** Per-tenant index isolation (revisit
  if this becomes multi-tenant).
- [ ] **Manual.** Move `.env`'s plaintext credentials to a real secrets
  manager (AWS Secrets Manager / SSM Parameter Store).

## B. Governance-signal monitoring

- [x] **Automated.** `make monitor` (`python -m src.monitor`, from `scripts/operationalize-production/`) —
  alerts on hallucinated citations, escalation-rate drift, and NER service
  downtime. Exits non-zero on any alert (safe to wire into cron/CI).

## C. Audit-log hardening

- [x] **Automated.** `ensure_audit_retention_policy()` (called by
  `validate.py`) attaches an OpenSearch ISM retention policy
  (`AUDIT_RETENTION_DAYS`, default 180d) to `recipes-audit-log`. The policy
  deletes the whole index when it reaches that age, not individual records.
- [ ] **Manual.** Apply the `audit_writer` / `audit_admin` role separation
  from `scripts/operationalize-production/src/hardening/opensearch_roles.json` so the audit trail can't be
  edited/purged by the same credential that writes to it.

## D. Governance-policy change control

- [x] **Automated.** `make policy` (`python -m src.policy_registry`, from `scripts/operationalize-production/`) —
  checks `.env`'s `TIER_MIN_HOT_SCORE` / `TIER_MIN_HOT_CANDIDATES` against
  the approved version in `scripts/operationalize-production/src/policy_history.json`
  and fails on undocumented drift.
- [ ] **Manual, ongoing.** Require code review on `assign_tier()`
  (`scripts/governance-tiering/src/ingest.py`) for any change to *which*
  field decides the hot/LT split — the automated check above only catches
  numeric threshold drift, not a qualitative rule change.

## E. General operational readiness

- [ ] **Manual.** Latency alarms — build from the `latency_ms` field
  already captured per-query in `recipes-audit-log` (Governance Tiering).
- [ ] **Manual.** Error-rate / cost alarms on Bedrock spend via AWS
  CloudWatch's `bedrock-runtime` metrics.

See [Operationalize Production's task page](../tasks/operationalize-production.md) for the
full rationale behind each item and how to run the automated checks.
