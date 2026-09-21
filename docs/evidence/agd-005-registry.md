# AGD-005 checkpoint — durable credential registry

Implemented 2026-09-21 under [ADR-0026](../adr/0026-durable-agentd-credential-registry.md).
AGD-005 remains open; this checkpoint supplies PostgreSQL identity bookkeeping,
not a complete authenticated admission/rotation/Secret delivery path.

## Verification

Tests use the pinned local PostgreSQL image and a separate development test
database. The existing default development database had an older schema and its
migration attempt failed; the subsequent successful migration/tests used the
separate database without resetting existing data.

- Full migration chain from an empty temporary schema, checksum ledger and
  idempotent reapplication, including migration 19's two credential tables.
- Cross-tenant/SandboxBinding/Attempt/certificate/expiry/proof rejection.
- Two concurrent bootstrap consumers: exactly one commits, and reopening the
  adapter still rejects replay.
- Competing renewal registrations: exactly one compare-and-swap succeeds.
- Current connection checks, stale epoch rejection, stale-close safety, exact
  close and no resurrection of a consumed bootstrap.
- Execution cancellation between version lookup and registration fails closed.
- Credential history cannot be rewritten; non-superuser RLS exposes only the
  active tenant's records in both new tables.

No private keys or plaintext proofs are persisted. Fixtures use synthetic digest
metadata and in-memory proof bytes; these tests exercise registry checks rather
than repeat the real TLS/certificate tests from AGD-003/004.

Reproduction uses the documented migration command and Go tests, setting
`THINKPIXELAR_DATABASE_URL` / `THINKPIXELAR_TEST_DATABASE_URL` to an isolated local
test database:

```sh
go run ./cmd/migrate up
go test -race ./internal/adapters/postgres ./internal/adapters/postgres/migrations -count=1
make verify
```

The complete PostgreSQL and migration race suites passed against the isolated
database. `make verify` passed: generated drift, hygiene, versions, formatting,
static analysis, unit/race tests, vulnerability scan (no vulnerabilities), license
checks, builds and OpenAPI checks. Staged whitespace and changed local Markdown
links also passed. No homelab deployment, complete wire admission, live Secret
cleanup or recovery/reconnect qualification is claimed.
