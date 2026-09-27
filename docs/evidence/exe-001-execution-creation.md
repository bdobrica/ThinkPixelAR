# EXE-001 — durable local Execution admission

Implemented 2026-09-27. `POST /v1/sessions/{session_id}/executions`
accepts `{"input":"..."}`, a scoped `Idempotency-Key`, and a quoted Session
state version in `If-Match`. It returns HTTP 201, `Location`, and a `QUEUED`
Execution at state version zero, including explicit local authority labels.

Composition supplies `http.Options.Executions`, `AuthenticateSession`, and an
`execution.NewLocalCreator` with current Session access authorization and a
configured `LocalAuthority`. Authentication derives tenant/principal from verified
credentials. The stock executable remains unconfigured and returns 401.

The transaction reserves the replay identity, locks a READY/IDLE Session, issues
and validates its durable local grant, verifies the immutable runtime binding,
advances the Session generation/current writer, and persists the Execution,
Confidential input, ordered Session/Execution events and
`execution.materialize.v1` outbox intent. Grant issuance joins this transaction;
a failure rolls back its snapshot and authority replay record too. This seam
is deliberately local: remote AG admission/claim requires separate composition.

Input lives in `execution_inputs`, with forced tenant RLS and a digest; it is not
copied into events, replay responses, logs or queue payloads. Queue consumers must
read it under the tenant scope, verify the digest, and revalidate current authority
and fences before starting work. Deployment encryption and input erasure/retention
remain deployment/lifecycle responsibilities. HTTP requests are limited to 256 KiB
including JSON encoding. Nonempty `run_reference` is currently rejected with 422.

Replay checks current Session access and returns the original result, including
after authority expiry or service restart. Replay grants no permission to execute.
Changed input or `If-Match` conflicts. Generic idempotency expiry retains creation
keys until resource/tombstone-aware erasure is implemented. Missing and inaccessible
Sessions return 404; stale lifecycle/version and competing writers return 409.

Migration 24 adds Confidential input storage and permits version-zero
`execution.accepted` events with Execution identity and no Attempt. OpenAPI uses
the existing domain states (`QUEUED`, `MATERIALIZING`, `TIMING_OUT`, etc.).

## Verification

`TestPostgresCreateExecutionHTTP` uses a real HTTP listener, PostgreSQL and
LocalAuthority with explicit test identity/qualification fixtures and seeded READY
Sessions. It covers concurrent identical requests, competing distinct keys, exact
replay/normalization, changed-input conflict, tenant isolation, revoked replay
access, restart/expired-authority replay, input/grant binding, creation-key retention,
and complete rollback after outbox failure. Seeding READY does not prove Workspace
provisioning.

Reproduce against an isolated PostgreSQL database after applying migrations:

```sh
export GOCACHE=/tmp/thinkpixelar-go-cache
# Set THINKPIXELAR_DATABASE_URL and THINKPIXELAR_TEST_DATABASE_URL to that database.
go run ./cmd/migrate up
go test -race ./internal/adapters/http -run 'TestCreateExecutionRequestBoundary|TestPostgresCreateExecutionHTTP' -count=1
```

Also verified: affected race tests, existing Session creation/grant lifecycle/
idempotency PostgreSQL regressions, affected `go vet`, internal package compilation,
and pinned Redocly OpenAPI lint/bundle. Migration 24 was applied only to an isolated
test database, which was removed afterward.

No Workspace, Attempt, sandbox or harness is started by this endpoint. Authentication
wiring, materialization consumer, status/input/cancel APIs, SSE, and AG authority
remain separate work. [EXE-002](exe-002-execution-binding.md) adds shared
binding verification and durable local-grant reconstruction; lifecycle consumer
composition remains separate.
