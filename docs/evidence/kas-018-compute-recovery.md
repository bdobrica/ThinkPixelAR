# KAS-018 — Missing and orphaned bound compute

Date: 2026-09-20.

Implemented atomic recovery effects and exact cleanup for missing/unverified or
stale persisted bindings. [ADR-0018](../adr/0018-compute-loss-recovery.md) records
the decision and scope. Recovery jobs are deliberately separate from compute jobs:
compute cleanup cannot authorize replay of an Execution or declare a Session ready.

Real PostgreSQL race tests prove:

- Missing bound compute leaves the Session DEGRADED, preserving its Execution and
  recovery state, with one recovery job, ordered event, outbox message and exact cleanup.
- Attempt mutations remain rejected while DEGRADED.
- Stale observation replay fails instead of duplicating recovery effects.
- A cancelling Execution's old compute is marked orphaned without changing Session state.
- Integrity failure during cleanup quarantines the intent and prevents further release authorization.
- Restart can load an already committed cleanup intent before the provider operation
  has been journaled, preserving its exact operation identity.
- Compute workers do not claim domain recovery work.

Migration 18 was applied to the isolated Phase 3 test database. The full real
PostgreSQL suite includes empty-schema migration and upgrade from the Phase 2
baseline; previous migrations were not edited. Commands: `go test -race
./internal/adapters/postgres/... ./internal/app/reconciliation/... -count=1` with
explicit test database configuration; `make verify`.

This does not implement global discovery or deletion of unbound Kubernetes objects,
full domain recovery execution, or automatic retry of ambiguous agent actions.
Those are separate operational/application workflows. The homelab RC scope and
future production qualification remain unchanged.
