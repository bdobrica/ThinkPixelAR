# AGD-013 rotation/reconnect evidence

Date: 2026-09-22

## Implemented scope

- Negotiated v1 Rotation REQUEST/ISSUED validation, authenticated fresh-key
  issuance, current-stream installation and in-memory replacement.
- Empty-proof reconnect with the latest current credential, durable connection
  ID/epoch replacement and rejection of stale frames/close callbacks.
- Explicit recovery policy/provider checks, post-expiry version-fenced bootstrap
  registration and same-configuration fresh-bootstrap loading.
- Finite reconnect loop, renewal scheduling, jittered retry bounds, deadline
  margin, local disconnect cleanup and ambiguous-handshake proof suppression.

## Verification

- Focused race suite passed for agentd, admission, issuance and all sandboxtransport
  adapters. Real TCP/mTLS tests perform rotation and reconnect, reject malformed
  rotation identity/key/expiry and replace a server instance without resetting
  epoch state. Expiry loading and lost-Welcome tests exercise recovery/failure.
- PostgreSQL race integration tests passed for `TestAgentd*`: durable admission,
  credential registration/consumption, restart replacement, concurrent epochs,
  old-close safety, wrong Attempt, expired/predecessor reconnect rejection and
  early/replayed recovery denial. Recovery uses a fresh proof and renewed
  credentials replace the current epoch.
- The existing development database migration command failed with suppressed
  details. No existing data was reset. An isolated `thinkpixelar_agd013` database
  in the existing local PostgreSQL container migrated successfully and was used
  for integration tests. No schema migration was added or changed by AGD-013.
- `make verify`: passed, including generation/formatting/static checks, full
  unit/race suites, vulnerability/dependency checks, builds and API checks.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c` for
  `./internal/app/agentd`: passed (compile only).

## Limits

External provider/authority decisions use explicit test fixtures; the durable
registry tests use real PostgreSQL. The TLS and database tests qualify their
respective boundaries, not a deployed end-to-end Kubernetes execution.

AGD-020 must wire the concrete frame/authority policies, renewal-channel handling,
heartbeat/event multiplexing, disconnect fencing and provider delivery/cleanup.
The current immutable Kubernetes bootstrap mount cannot refresh in place; expired
identity without protected recovery delivery fails closed for AR replacement.
No live homelab same-Pod recovery, new infrastructure cost or paid service is
claimed. See [ADR-0037](../adr/0037-agentd-rotation-reconnect-recovery.md).
