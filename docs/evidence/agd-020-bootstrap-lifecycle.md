# AGD-020 — bootstrap publication and cleanup lifecycle

Date: 2026-09-22. Bootstrap orchestration/worker step complete; AGD-020 remains open.

## Verified path

TestAgentdBootstrapLifecycle composes actual agentdidentity issuance, dedicated
client/server CA roles, local admission policies, PostgreSQL credential and delivery
stores, bootstrap.Delivery, agentdbootstrap.Service and the running cleanup worker.
Only Kubernetes/provider observations use test doubles. The Secret API double
requires exact UID and resourceVersion deletion preconditions.

The projection callback observes an already persisted provider UID. Acceptance
atomically schedules cleanup before returning the lease. Invalid proofs do not
schedule deletion. A provider failure in the post-consumption admission recheck
still leaves cleanup durable. Projection/acquisition callback failure cleans the
Secret. A genuinely expired short-lived credential becomes eligible by database
time and is removed by the worker. Reconstructed coordinator/journal instances
clean consumed, revoked and expired material without fresh execution authority.

Unit tests cover credential buffer destruction, publication/projection errors,
cancellation-independent cleanup, finite callback/worker deadlines, copied tenant
allowlists, continuation across tenants, and periodic retry after a timed-out pass.
No additional dependency or schema migration is introduced.

## Validation

Passed against a disposable database migrated through 0021:

```sh
# THINKPIXELAR_TEST_DATABASE_URL points to the disposable database.
go test -race ./internal/adapters/postgres -run 'TestAgentdBootstrapLifecycle|TestAgentdDelivery|TestAgentdCredential|TestAgentdAdmission|TestAgentdPolicy' -count=1
go test -race ./internal/adapters/postgres -run TestAgentdBootstrapLifecycle -count=3
```

The lifecycle test runs the actual worker Run loop; it does not start a deployed
worker. No live Kubernetes Secret qualification, binary listener/worker startup,
sandbox replacement or integrated controlled-process acceptance is claimed.

`make verify` passed: formatting, static analysis, unit/race tests (including the
worker's periodic retry test), vulnerability/license checks, builds and generated
Protobuf/OpenAPI checks. Staged whitespace, repository hygiene and local Markdown
links passed. The disposable database was removed after tests; no existing
application or homelab database was changed.
