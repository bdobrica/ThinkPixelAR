# EXE-003: Execution status retrieval

`GET /v1/executions/{execution_id}` returns the persisted Execution state,
state version, Session generation, creation time, and immutable authority
mode/issuer. An external Run ID is included when present. The response uses the
existing OpenAPI `Execution` schema and excludes input, grants, runtime evidence,
terminal evidence, and sandbox observations.

Composition supplies `execution.NewReader(store, readAccess)` through HTTP
`Options.ExecutionReader`, together with the existing trusted
`AuthenticateSession` callback. `ReadAccess` checks current disclosure policy
using the authenticated caller and persisted Session/Execution IDs on every
request. Creation permission is not reused as read permission. Persistence is
tenant scoped; missing and inaccessible resources return 404. Responses are
`Cache-Control: no-store`; internal failures return safe 503 problems.

Reads do not issue or validate execution authority, infer terminal state from
expiry, or require the Execution to be the Session's current generation. Historical
state remains readable after cancellation or a later generation, subject to
current disclosure policy. Labels come from the stored binding, not the currently
configured authority adapter.

Verification performed:

- `go test -race ./internal/app/execution ./internal/adapters/http`: all persisted
  lifecycle states, historical AG labels/Run reference, exact metadata projection,
  authentication/configuration failures, malformed IDs, denied/missing reads,
  and safe database errors. PostgreSQL tests skip without a database URL.
- `go test -race ./internal/adapters/http -run TestPostgresCreateExecutionHTTP -count=1`
  with `THINKPIXELAR_TEST_DATABASE_URL` pointing to an isolated migrated PostgreSQL
  database: real TCP create/read, denied and cross-tenant reads, missing records,
  and old terminal/new queued generations after grant cancellation.
- Focused `go vet`; OpenAPI validation and regeneration.

The stock executable still requires API-001/API-002 trusted authentication and
policy composition; it fails closed without these dependencies. This change does
not implement workers, lifecycle transitions, signals, cancellation, or SSE.
The terminal transition in the database test is an explicit fixture.
