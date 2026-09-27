# EVT-001 — Session SSE

Implemented `GET /v1/sessions/{session_id}/events/stream` over tenant-scoped
PostgreSQL history. The stream uses decimal Session sequence IDs and the
published RuntimeEvent envelope. It never grants or renews execution authority.
See [HTTP conventions](../api/http-conventions.md#implemented-session-stream-evt-001)
for limits, retention behavior and required trusted composition.

Verification on 2026-09-27:

- Focused race tests for the reader, HTTP adapter and PostgreSQL adapter.
- Real TCP HTTP/PostgreSQL test `TestPostgresSessionStreamHTTP` against a separate
  database with all 25 repository migrations: Session creation, ordered replay,
  heartbeat, live append, HTTP/2 reconnect through a fresh reader/server with
  heartbeat interval longer than the write timeout, tenant denial,
  permission and credential revocation, future cursor rejection, interior
  retention gap, bounded connection lifetime and stalled-writer deadline/quota
  release (the stalled writer is a controlled socket-behavior fixture).
- Reader tests cover expired prefix/tail/all history, interior holes, empty/caught
  up streams and event-specific disclosure denial. HTTP tests cover cursor syntax
  and quota release.
- Affected `go vet`, OpenAPI lint and generated bundle.

Reproduce the integration check with a migrated disposable database:

```sh
THINKPIXELAR_TEST_DATABASE_URL='<isolated PostgreSQL URL>' \
  go test -race ./internal/adapters/http -run TestPostgresSessionStreamHTTP -count=1
```

Authentication and payload disclosure in this test are explicit trusted fixtures.
No development authentication bypass was added. Production/executable identity
and disclosure wiring, harness-candidate publication, Execution streams and
physical retention erasure remain separate work. Logical retention is enforced
at read time; no event deletion or migration was introduced. This is not evidence
of a live Codex-to-SSE stream or shared multi-replica connection quotas.
