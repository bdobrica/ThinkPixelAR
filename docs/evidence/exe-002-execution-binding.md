# EXE-002 — Immutable Execution authority/runtime binding

Implemented 2026-09-27 for local Execution admission. EXE-001 already stores the
unique grant reference/digest and exact Session runtime evidence atomically with
Execution creation. The same integrity verifier now checks admission and
`execution.LoadLocalBinding` reconstruction from tenant-scoped repositories.

The verifier checks the exact persisted grant bytes/digest, issuer/mode, tenant,
Session, generation, agent/version, full runtime binding, evidence digest, and
immutable deadline. It reconstructs the typed evidence encoding across PostgreSQL
JSONB formatting and compares timestamps at PostgreSQL microsecond precision.
Unknown evidence fields and mismatched snapshots fail closed. No current catalog
lookup, mutable agent alias resolution, or replacement admission occurs on load.
Existing database triggers protect Execution identity/runtime/deadline, and the
unique authority reference prevents reusing a grant for another Execution.

Loading proves historical integrity, **not current authority**. Trusted consumers
must authorize the tenant/operation, call `LocalLifecycle.Validate`, and enforce
current Session/Attempt fences before forward work. Cancelled/expired historical
grants remain readable; neither loading nor API idempotency replay renews them.
AG admission, materialization workers, terminal lifecycle composition, and public
authentication wiring remain separate work. No new schema or public API is added.

## Verification

Run against an isolated PostgreSQL database with existing migrations applied:

```sh
# Set THINKPIXELAR_DATABASE_URL and THINKPIXELAR_TEST_DATABASE_URL to that database.
go run ./cmd/migrate up
go test -race ./internal/app/execution ./internal/adapters/http ./internal/adapters/authority/local
go vet ./internal/app/execution ./internal/adapters/http
```

Verified focused race tests and vet, plus `TestPostgresCreateExecutionHTTP` with
real HTTP/PostgreSQL/LocalAuthority. The integration test reconstructs an admitted
Execution through a new store, verifies detached grant copies and tenant isolation,
checks ACTIVE then CANCELLED authority separately from unchanged historical
binding, rejects database binding edits, and admits a fresh later generation while
retaining the original history. READY and terminal Session states are test
fixtures; these checks do not demonstrate Workspace provisioning or compute.

`TestLocalBindingIntegrity` rejects swapped grant identities/digests, generations,
agent versions, modes, external Run claims, altered evidence, tenant/Session
substitution, future issuance, and changed deadlines. JSONB formatting and
cancelled history remain readable.
