# AGD-005 checkpoint — protected bootstrap Secret operations

Implemented 2026-09-21 under [ADR-0028](../adr/0028-agentd-bootstrap-secret-projection.md).

## Verification

- Ephemeral in-memory client/server CAs and issued bootstrap certificates exercise
  exact key/certificate/identity/proof registration matching and bounded files.
- Invalid key/config/proof/challenge, cross-Attempt record, expired/overlong
  bootstrap, wrong issuer/digest, invalid/trailing trust data and shared-role
  client/server trust fail before a Kubernetes operation.
- Immutable create-or-verify replay preserves the same object; mismatched UID,
  ownership, data, digest, namespace or immutability cannot resolve or delete.
- UID and resourceVersion deletion preconditions are checked by the API double;
  conflict errors are sanitized, and deletion is idempotent after absence.
- A consumed registration prevents resolution but still permits exact cleanup.
  A fence failure after creation attempts exact cleanup and returns its known
  identity for retry. Ambiguous-create recovery requires matching planned content;
  missing objects have a distinct absence result.
- Real PostgreSQL tests verify publication eligibility for the exact registered
  record and rejection of a modified record or one whose proof was consumed.

The Kubernetes tests use a narrow API double, not a live API server. All test key
material is generated in memory; no valid credentials or Secret manifests are
stored in fixtures. No new dependency or cluster change is introduced.

Focused Secret lifecycle race tests and the real PostgreSQL registry tests passed.
`make verify` passed: generated drift, hygiene, versions, formatting, static
analysis, repository-wide unit/race tests, vulnerability scan, license checks,
builds and OpenAPI checks. Staged whitespace and local Markdown link checks also
passed. AGD-005 remains open:
durable plan/UID/cleanup orchestration, credential loading, concrete policy and
rotation composition are not supplied by this checkpoint. The binary remains
dormant; no end-to-end bootstrap or homelab cleanup qualification is claimed.

Reproduction: [bootstrap operations](../operations/agentd-bootstrap-secrets.md).
