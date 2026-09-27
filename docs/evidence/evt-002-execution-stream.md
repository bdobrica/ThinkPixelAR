# EVT-002: Execution event streaming

Verified on 2026-09-27 using an isolated PostgreSQL database with all existing
migrations applied. No schema migration or new dependency was required.

The `ExecutionSSE` subtest of `TestPostgresCreateExecutionHTTP` uses two real
Execution generations in one Session and cancelled historical local authority.
Over real TLS HTTP/2, it checks original Session sequence IDs, exact Execution
filtering, resumed catch-up, live append, access revocation/disconnect, tenant
isolation, malformed/future cursors and an expired Session-only event causing
an explicit retention gap. Fixtures supply trusted authentication/disclosure;
they do not claim production identity wiring or harness event publication.
The existing Session SSE integration test also passed after transport reuse.

Verification:

```sh
GOCACHE=/tmp/thinkpixelar-go-cache go test -race ./internal/app/eventstream ./internal/adapters/http
THINKPIXELAR_TEST_DATABASE_URL='<isolated migrated PostgreSQL URL>' \
  GOCACHE=/tmp/thinkpixelar-go-cache go test -race ./internal/adapters/http \
  -run 'TestPostgres(CreateExecution|SessionStream)HTTP' -count=1
go vet ./internal/app/eventstream ./internal/adapters/http
```

Pinned Redocly 2.49.0 validated the OpenAPI source and regenerated its bundle.
Focused reader tests additionally check mandatory Execution policy, payload
policy denial and omission of unrelated payloads before disclosure callbacks.
The shared transport's existing tests cover heartbeats, HTTP/2 idle deadlines,
write bounds, connection lifetime and quota release.

Filtering scans one underlying Session event per transaction. Missing history
fails conservatively even if it might have belonged to a different Execution.
Large unrelated histories therefore require additional bounded reads. Executable
authentication/disclosure wiring and live harness publication remain pending.
