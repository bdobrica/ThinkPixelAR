# AGD-020 progress — durable bootstrap delivery

Date: 2026-09-22. AGD-020 remains open.

Implements [ADR-0038](../adr/0038-durable-agentd-bootstrap-delivery.md): the delivery
coordinator and PostgreSQL journal commit plan/cleanup eligibility before external
publication and UID before projection. Publication is single-winner; ambiguous
and failed delivery remain tracked for cleanup, including delayed Secret creation.
No key, proof or bundle contents are persisted.

## Verification

- Bootstrap coordinator and PostgreSQL package race tests: passed (database tests
  require the explicit test database variable; the separate live run is below).
- Real PostgreSQL 18.6 in the existing local container: isolated database
  `thinkpixelar_agd020_delivery`, complete migration chain applied successfully;
  empty-schema migration/reapplication test passed.
- Live race tests for delivery restart persistence, concurrent publication claims,
  cross-tenant RLS, exact credential metadata, UID conflicts, pending-work selection, irreversible cleanup
  and registry compatibility: passed.
- `make verify`: passed after the final credential-binding review fix.

The Kubernetes API is a narrow explicit test double in coordinator tests. No
live cluster Secret deployment or authenticated controlled-process exchange was
performed. The disposable test database was removed after verification. The existing
development database was not migrated or reset.

## Remaining AGD-020 work

Executable materialization/admission/cleanup-worker wiring must call this
coordinator; it is not scheduled automatically by adding the journal. Concrete
bounded authority/materialization and command/report policies, durable command
replay reconciliation, AR/agentd binary wiring, event/heartbeat/renewal dispatch,
stop/fencing, effective Pod shutdown-budget validation and the authenticated
controlled-process acceptance test remain required. AGD-019 stays open.
