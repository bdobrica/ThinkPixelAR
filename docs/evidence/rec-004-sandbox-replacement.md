# REC-004: pre-execution Sandbox replacement

Implemented 2026-09-27. REC-004 remains open for running-work recovery and
executable integration. No live Kubernetes deletion or checkpoint restoration is
claimed by this change.

`SandboxReplacements.ReplaceDeleted` atomically replaces a safely lost
pre-execution Attempt and reserves its fresh Sandbox. Migration 32 retains the
immutable replacement decision. `recovery.SandboxReplacement` hands acquisition
to the existing compute reconciler; provider calls run outside the transaction.
Policy is mandatory and has no permissive production default. Trusted materialization
allocates the candidate Attempt/Sandbox/acquisition IDs before preparing their
scoped resource references; every retry supplies the same tuple.

Real PostgreSQL checks cover concurrent callers producing one replacement,
unchanged Session epoch/runtime/Workspace generation/deadline, fresh identities,
old-Attempt heartbeat/acquisition rejection, authorized retries after restart,
conflicting references, tenant isolation, immutable decision history, cancellation,
policy denial, incomplete cleanup, dispatched commands, running Executions, old
connections, live bootstrap delivery, and another recovery worker's active lease.
Denied admission leaves the Session degraded with no partial candidate. Successful
admission commits ordered events/outbox and transfers the loss job to compute work.
A fault-injecting provider with real AR persistence/reconciliation simulates a
create with a lost response; restarted AR objects reuse that candidate and converge
to fixture READY without a second acquisition. Transient provider unavailability
also retries the retained candidate.

Verification uses a disposable database with all 32 migrations applied:

```sh
GOCACHE=/tmp/thinkpixelar-go-cache go test -race ./internal/adapters/postgres \
  -run 'TestSandboxReplacement|TestMissingCompute|TestComputeQueue|TestSandboxBindingsDurableReplay' -count=1
GOCACHE=/tmp/thinkpixelar-go-cache go test ./internal/adapters/postgres/migrations
GOCACHE=/tmp/thinkpixelar-go-cache go vet ./internal/app/recovery ./internal/adapters/postgres
```

The PostgreSQL tests require `THINKPIXELAR_TEST_DATABASE_URL`. Provider observations
and policy approval are test fixtures. Trusted authority/lease, runtime revocation,
retry-budget, attachment and credential-verification composition and the executable
recovery worker remain necessary for deployment. Running-work recovery requires
validated checkpoint restoration and external-outcome reconciliation; this lane
refuses any Execution with recorded commands instead of retrying ambiguous effects.
