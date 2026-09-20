# KAS-006 — Exact, idempotent release

Date: 2026-09-20.

Release records its operation through the durable lifecycle store seam before
external mutation. The store contract rejects conflicting digests and superseded
operations, preserving the latest desired mutation revision. Deletion reads the
exact binding and uses both UID and resource-version preconditions with foreground
propagation. It never deletes by a tenant/session selector. Missing owned compute
is successful replay; authoritative binding/history is retained. Acceptance does
not claim physical absence; status remains the cleanup confirmation boundary.

Passed focused race tests and `make verify`: exact deletion preconditions,
foreground propagation, absent/repeated delete, retained binding, replacement UID
and invalid operation rejection. Production lifecycle-store wiring remains part
of KAS-010/017. No live deletion qualification is claimed here.
