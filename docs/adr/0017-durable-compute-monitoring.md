# ADR-0017: Persist one leased compute monitor per SandboxBinding

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

An AR restart must not lose acquisition/release intent, and a READY observation
must not stop detection of later compute loss. Existing PostgreSQL reconciliation
work already provides tenant isolation, unique targets and monotonic claim fences.

## Decision

Atomically enqueue one `sandbox.reconcile` work item when reserving acquisition
or recording release intent. Use SandboxBinding ID as stable work ID/target;
provider operation IDs remain separate in the immutable acquisition/lifecycle
journal. Replay verifies queue identity rather than creating another job.

The PostgreSQL compute store loads the saved request and latest supported desired
operation under Session/Execution/Attempt and binding locks. Running compute
requires the current fence; release requires an exact durable cleanup record.
Observation writes repeat these checks, including binding version and operation
revision. Store only bounded reasons and a digest of verified effective facts.
Confirm exact cleanup in the same transaction that records physical absence.

Workers claim only compute work using database time, SKIP LOCKED and bounded
leases/batches. Each provider reconciliation gets a deadline bounded by its claim.
Expired or superseded workers cannot finish/reschedule work. READY is periodically
rescheduled under the same identity; only confirmed RELEASED completes monitoring.
External calls occur outside database transactions. No new database schema or
external scheduler is needed.

## Alternatives considered

In-memory queues lose intent at restart. A job per poll loses lineage and grows
without bound. Completing monitoring at READY misses later loss. Reusing a worker
lease as aggregate authority permits stale results. Native suspend/resume journals
are not silently interpreted as Session restore authorization.

## Consequences

Binding a provider UID during acquisition may advance the binding version before
the original observation commits. That observation is safely rejected; the same
monitor retries and reads the saved UID. Work survives response loss without
allocating a new Sandbox identity. Service composition must supply trusted tenant
selection, a real current-authority adapter and the provider; this change does not
start an unconfigured worker in the service binary.

## Security

PostgreSQL rechecks current Session/Execution/Attempt state and exact cleanup
ownership independently of worker claims. Tenant predicates and existing forced
RLS remain mandatory. Cleanup does not renew execution authority. Native suspension
requires its later application workflow and currently returns UNSUPPORTED to this
cold-compute monitor. Unknown/error outcomes never imply absence.

## Operations

Use bounded retries and worker identities. Expired claims replay with a higher
claim fence after restart. No destructive reset is needed. KAS-018 supplies durable
recovery effects when the monitor discovers missing/unexpected compute.

## Compatibility

Reuses existing tables and immutable operation records; no migration, public
contract change or additional infrastructure dependency. Existing unrelated work
kinds are not claimed by compute workers.

## References

- [Compute reconciliation](0016-compute-reconciliation.md)
- [Persistence contract](../contracts/postgresql-persistence.md)
- [KAS-017 evidence](../evidence/kas-017-durable-reconciliation.md)
