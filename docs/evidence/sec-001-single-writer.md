# SEC-001: Session single-writer enforcement

Verified 2026-09-27 against a disposable local PostgreSQL database with all 33
migrations applied.

Existing partial unique indexes enforce one mutable Execution per Session and
one current Attempt per Execution. Admission locks the Session and atomically
commits its generation, Execution, local grant, input, events and outbox intent.

Migration 33 closes a concurrent lifecycle race in the Attempt mutation trigger:
it now locks the parent Session and Execution before evaluating the fence. The
repository acquires those locks before writing the Attempt, preserving aggregate
lock order. This prevents a stale snapshot of ACTIVE/mutable parents from
authorizing a write after another transaction degrades or terminalizes them.
Existing tenant, generation, current identity and optimistic-version predicates
remain in force; this does not introduce or widen execution authority.

Executable evidence:

- 256 concurrent HTTP requests with distinct idempotency keys elect one Execution
  and advance the Session generation once; losers receive conflict.
- 32 competing current Attempts elect one writer.
- Direct SQL heartbeat writes block on an uncommitted Session degradation or
  Execution terminalization. Tests observe PostgreSQL's actual lock wait, commit
  the parent change, and verify fence rejection with no partial heartbeat/version
  update.
- Existing optimistic-write, replacement/replay, missing-compute, Workspace
  checkpoint and checkpoint-publication checks pass with the new trigger.

Commands run (database URLs supplied through environment variables):

```sh
go run ./cmd/migrate up
go test ./internal/adapters/postgres/migrations ./internal/adapters/postgres ./internal/adapters/http
go test -race ./internal/adapters/postgres ./internal/adapters/http \
  -run 'TestAttemptFence|TestConcurrentCurrentAttempt|TestStoreConcurrencyInvariants|TestSandboxReplacement|TestMissingCompute|TestPostgresCreateExecutionHTTP|TestCheckpointPublication|TestWorkspaceCheckpoint' -count=1
go vet ./internal/adapters/postgres ./internal/adapters/http
```

The race run used real PostgreSQL and HTTP with fixture authentication/local
policy. No live Kubernetes storage fencing or AG lease integration was exercised.
Database fencing rejects authoritative stale writes; physical Sandbox shutdown
and storage attachment fencing remain necessary before allowing a replacement
Workspace writer. This evidence does not claim the complete standalone MVP flow.
