# AGD-004 — credential issuance and renewal

Implemented 2026-09-21 under [ADR-0025](../adr/0025-agentd-credential-issuance.md).

## Scope

Added a dedicated client-only P-256 CA adapter and a trusted application service
for bounded bootstrap and fresh-key renewal. Both authorization and registration
are mandatory ports; no permissive production authority exists. Registration
contains only non-secret IDs/digests/validity. Secret material is returned only
after registration succeeds and has explicit best-effort destruction.

No new runtime dependency or infrastructure was introduced. Existing public API
and protobuf definitions are unchanged. AGD-005 owns concrete durable admission,
one-time proof consumption, Secret projection/cleanup, and rotation stream wiring.
The binary remains dormant and the transport rejects unwired rotation messages.

## Verification

- Focused race tests use real in-memory P-256 CAs/keys and X.509 validation.
- Bootstrap scope, proof hash, ten-minute ceiling, fresh-key renewal and
  fifteen-minute ceiling; current authority, Attempt, bootstrap and CA deadlines.
- Unrestricted/server-role/mismatched CA rejection; invalid identity/domain,
  expired/future/backdated/unbounded issuance, cancellation and absent dependencies.
- Denied/revoked/stale/expired renewal; revocation between authorization and
  registration; no secret delivery on failed registration and clearing of its key.
- Concurrent renewals synchronized before registration: only one fake-authority
  compare-and-swap succeeds. This tests the required port interaction, not a
  production database implementation.
- Replacing the issuer with overlapping public roots validates both live chains;
  removal of the old root rejects its chain. This is local X.509 qualification,
  not live root distribution or active-stream revocation evidence.
- Redacted ordinary Go formatting and clearing of owned delivery buffers.

`make verify` passed: generated drift, repository hygiene, supported versions,
formatting, vet/Staticcheck, repository-wide unit/race tests, vulnerability scan
(no vulnerabilities), dependency/license checks, binary builds and OpenAPI checks.
Changed Markdown local links and the staged diff whitespace check also passed.
No live cluster, Secret cleanup, restart/reconnect or complete rotation exchange
is claimed here.

Reproduction: [credential operations](../operations/agentd-credentials.md).
