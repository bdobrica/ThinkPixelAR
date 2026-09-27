# SES-004 Session suspend

Implemented 2026-09-27. Scope: the standalone authoritative suspend boundary from
READY/IDLE, using a fresh detached Workspace checkpoint. No public route or live
provider qualification is claimed.

`internal/app/session/suspend.go` defines the request/result and caller-bound
replay digest. `internal/adapters/postgres/session_suspend.go` validates the exact
checkpoint while holding the Session and durable-object locks, excludes mutable
Executions, and atomically commits suspension, immutable operation history,
event/outbox, agentd connection fencing, exact compute release and bootstrap
Secret cleanup intents. Migration 30 adds the tenant-isolated immutable history.
CHK-002 validation is shared within the transaction, avoiding a validation/commit
gap. No new dependencies, runtime binding changes or execution authority issuance.

Verified with a disposable PostgreSQL database after applying all 30 migrations:

- READY without compute and IDLE with a terminal Execution and retained compute;
- concurrent duplicate requests produce one operation/event/outbox;
- final outbox-write failure rolls back state, history, connection fencing and cleanup;
- process-object replacement and replay after a later lifecycle transition;
- changed request/version, unauthorized replay and wrong tenant;
- active or otherwise mutable Execution, attachment, snapshot in progress,
  missing/deleting checkpoint and failed trusted boundary verification;
- concurrent admission waits for the suspend lock and then rejects SUSPENDED;
- exact retained compute release intent, cleared agentd stream projection and
  bootstrap cleanup request; failed release followed by confirmed absence;
- CHK-001 publication and CHK-002 integrity/compatibility regression tests.

Focused checks:

```sh
# Supply THINKPIXELAR_TEST_DATABASE_URL for a migrated disposable PostgreSQL DB.
go test -race ./internal/adapters/postgres ./internal/app/session ./internal/app/checkpoint ./internal/adapters/postgres/migrations -run 'TestSessionSuspend|TestSuspendRequest|TestCheckpointRestore|TestCheckpointPublication|TestRestore|TestManifest|TestLoad' -count=1
go vet ./internal/app/session ./internal/adapters/postgres
```

Tests use real database transactions and signed manifests with streamed vendor
bytes. Quiescence, external credential revocation, storage retention and runtime
eligibility callbacks are explicit fixtures. Retained compute and Secret references
are database fixtures; release observations exercise reconciliation persistence,
not Kubernetes deletion. Concrete adapters, HTTP/worker composition, live storage
snapshot/detachment, and cold resume remain pending. The preparation worker must
preserve quiescence through checkpoint publication and suspend; an old active
checkpoint alone cannot authorize this release boundary.
