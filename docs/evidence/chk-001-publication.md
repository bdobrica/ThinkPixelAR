# CHK-001 — signed durable Checkpoint publication

The standalone publisher now joins an exact committed WSP-003 Workspace boundary
to independently verified vendor objects and signs a schema-validated RFC 8785
manifest. Current authorization and trusted verification are mandatory injected
dependencies. Signing uses Ed25519; no signing keys enter Workspace/vendor state.

PostgreSQL integration tests exercise real WSP preparation/generation publication
followed by Checkpoint publication. They cover missing generation, failed object
verification/signing, stale Attempt/cancellation/attachment, runtime/profile/epoch/
lineage/tenant mismatch, concurrent retries, process replacement, changed input,
current disclosure denial, immutable request digest, detached publication, and
historical replay after a newer generation/Checkpoint. An injected collision on
the final outbox insert verifies rollback of Checkpoint, Session pointer and event.
Manifest tests independently recompute payload digests and verify signatures,
check canonical deterministic encoding and mutation binding, and reject duplicate
objects, unsafe integers, credential-bearing references and malformed signatures.

Verification (2026-09-27):

- Applied migrations through 29 to disposable PostgreSQL database
  `thinkpixelar_chk001`.
- `go test -race ./internal/app/checkpoint ./internal/domain/checkpoint ./internal/ports/checkpoint`
- With `THINKPIXELAR_TEST_DATABASE_URL` pointing to that database:
  `go test -race ./internal/adapters/postgres ./internal/adapters/postgres/migrations -run 'TestCheckpointPublication|TestWorkspaceCheckpoint|TestLoadReturns|TestUpFromEmptyPostgreSQL' -count=1`
- `go vet ./internal/app/checkpoint ./internal/ports/checkpoint ./internal/adapters/postgres ./internal/adapters/postgres/migrations`

Integration tests use fixture storage verifiers and ephemeral real Ed25519 keys.
They do not establish physical storage durability, credential scanning quality,
provider retention, or live Codex reconstruction. Concrete WSP-004 snapshots,
independent object verification/pinning, operator signer/key policy and executable
worker composition remain required. CHK-002 restore validation, Session suspend
and compute release are not implemented by this change. No public API route,
authority grant or cross-component ownership change is introduced.
