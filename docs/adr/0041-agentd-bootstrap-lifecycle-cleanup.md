# ADR-0041: Bootstrap materialization and tenant cleanup lifecycle

Status: Accepted 2026-09-22

## Decision

Compose registered issuance with ADR-0038's durable Delivery coordinator in
agentdbootstrap.Service. Materialize issues once, publishes through Delivery,
and passes only the resolved Secret name to a trusted projection/acquisition
callback. The callback must persist its projection binding before acquisition
and honor its context. Config and challenge are trusted materialization inputs
and must match the registered ADR-0040 snapshot; they are not sandbox inputs.

The callback runs only after publication has durably recorded the plan and UID.
Its context cannot outlive the bootstrap credential. Publication, projection or
acquisition failure/cancellation attempts Delivery.Cleanup with an independent
five-second context. Cleanup records irreversible intent before exact deletion;
the already persisted expiry plan is the fallback if immediate cleanup fails.
No automatic issuance or acquisition retry is introduced. Owned issued certificate,
key and proof buffers are destroyed on every return, including failure.

PostgreSQL bootstrap consumption now marks the corresponding delivery for cleanup
in the same transaction that consumes the proof and advances the connection epoch.
This makes cleanup eligible immediately and survives AR loss, lost Welcome, or a
subsequent failed admission recheck. Invalid proofs never reach the cleanup update.
Rejection before consumption does not delete a still-valid bootstrap: authoritative
materialization failure or expiry handles its lifecycle. No additional SQL table or
migration is required. This change does not turn Secret deletion into revocation.

## Worker

A runnable tenant cleanup worker calls the existing Delivery.Sweep immediately and
then periodically. Its immutable explicit allowlist has 1–128 distinct tenant IDs;
there is no tenant discovery or Kubernetes list. Configuration bounds the interval
to 5–60 seconds, each tenant pass to a positive maximum 30-second budget, and each
batch to 1–128 entries. One Run per worker instance is allowed. Store/provider calls
must honor context. Cancellation ends the loop; failure logs a fixed diagnostic
and does not stop retries or skip other tenants.

Expiry selection still uses PostgreSQL time. Existing five-second retry fairness,
unknown-UID late-create recovery, exact ownership checks and UID/resourceVersion
delete preconditions remain intact. A restarted worker reconstructs work from the
journal and needs no active grant to remove consumed, revoked or expired material.

## Scope

This implements the callable materialization composition, atomic acceptance trigger
and running worker loop. The service binary still needs to host them alongside its
transport listener in AGD-020's next wiring step. It does not deploy a controller,
change the homelab, or implement sandbox replacement/recovery. Integration tests use
real PostgreSQL, issuance/admission/local policies and the coordinator with a narrow
Kubernetes API double. See [evidence](../evidence/agd-020-bootstrap-lifecycle.md).
