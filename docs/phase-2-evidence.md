# Phase 2 authoritative persistence and domain-state evidence

- Date: 2026-09-03
- Scope: `DB-001` through `DB-023`
- Implementation baseline before this evidence commit: `fdb2eee`
- Result: Phase 2's authoritative persistence and domain-state exit gate passed.

## Delivered persistence boundary

| Area | Evidence-bearing implementation commits |
| --- | --- |
| Tenant/Session migrations and Session lifecycle | `b17b3f0`, `04a5af6`, `5e345f7` |
| Execution, Attempt, uniqueness, and generation fencing | `2e4a535`, `7b0b1d7`, `a34955a`, `de01377` |
| Workspace, Checkpoint, Runtime Profile, and runtime-binding metadata | `ccf62e0`, `eb2d1df`, `5fe2a68`, `41b8749` |
| Ordered Runtime Events and transactional repositories | `49b4812`, `1853d94` |
| Idempotency, outbox, reconciliation leases, and cleanup intent | `9b3bae9`, `8bcf63f`, `ef1f972`, `88c9a41` |
| Real-PostgreSQL migration, rollback, isolation, concurrency, and replay gates | `81bf4c1`, `4448cbb`, `b5062e4`, `0a26bd9`, `fdb2eee` |
| Generated lifecycle transition coverage | `ecea020` |

The domain packages remain independent of PostgreSQL and provider-specific
types. The persistence port requires explicit tenant-bound transactions, while
the PostgreSQL adapter owns SQL, row restoration, optimistic write predicates,
claim locking, and error classification. PostgreSQL remains authoritative for
lifecycle state and fencing; claim leases coordinate work without granting
aggregate mutation authority.

The 15 ordered embedded migrations create the tenant-qualified lifecycle,
metadata, event, idempotency, outbox, reconciliation, and cleanup schema. Their
foreign keys, partial unique indexes, checks, row-level-security policies, and
immutability/fencing triggers provide database-level defense in depth for the
invariants defined by the Phase 0 persistence and lifecycle contracts.

The DB-023 exit run found the newly published GO-2026-5004 advisory reachable
through the original `pgx` `v5.7.6` dependency. The exit change advances the
driver to the first fixed upstream release, `v5.9.2`, refreshes its checksum and
reviewed license inventory, and records the rationale in the DB-001 dependency
review. The post-update vulnerability scan reports no reachable vulnerability.

## Exit-gate evidence

| Phase 2 exit criterion | Evidence |
| --- | --- |
| Real PostgreSQL and migrations | The empty-schema gate applies all embedded migrations to pinned PostgreSQL 18.6, validates the ledger/checksums/tool version and resulting tables, then proves idempotent reapplication. |
| Tenant isolation | Colliding identities in two tenants exercise every repository's reads, writes, lists, and worker claims without cross-tenant disclosure or mutation. |
| State transitions | Table-driven and generated Session, Execution, and Attempt command sequences independently check legal edges, illegal-transition atomicity, terminal immutability, monotonic versions, and generation/current-attempt rules. |
| Event ordering | Concurrent appends serialize per Session and allocate a gap-free unique sequence while preserving tenant and Execution/Attempt lineage. |
| Concurrency rules | Real-database contention proves one mutable Execution per Session, one idempotency winner with stable replay identity, optimistic Attempt fencing, and exclusive work claims. |
| Fencing | Session generations, Attempt versions/current designation, idempotency owners, outbox claims, reconciliation claims, and cleanup intents reject stale mutation. |
| Rollback | Forced callback failure after authority-reference reservation, Runtime Event append, and outbox insert leaves no partial writes and permits safe reference reuse. |
| Replay | A new database connection/store cannot steal live leases, can reclaim expired outbox/reconciliation work with stable identities and higher fences, and cannot reclaim terminal work. |

The reproducible real-database command surface is:

```text
make test-db-migrations
make test-db-transactions
make test-db-tenant-isolation
make test-db-concurrency
make test-db-restart-replay
```

These targets use the pinned Compose PostgreSQL service and are also separate
least-privilege CI steps. Source-wide unit, generated-sequence, race, static,
dependency, build, hygiene, and OpenAPI checks remain covered by `make verify`.
DB-023 reran `make verify` and all five real-database targets after the driver
update; every gate passed.

## Qualification boundary

Phase 2 proves the persistence and domain-state exit criteria; it is not a
production-readiness claim. Provider-side idempotency and recovery cannot be
qualified before the Phase 3 sandbox adapter exists. Production-role privilege
and adversarial RLS qualification, retention/deletion workflows, schema
upgrade/rollback rehearsals, backup/PITR restore, operational observability,
load limits, and disaster game days remain owned by later security, operations,
and release-candidate items. No Kubernetes, Sandbox, harness, Workspace storage,
public API, or ThinkPixel integration behavior is implied by this evidence.

## Commit protocol

This evidence is committed before DB-023 tracking metadata so `TODO.md` and
`PLAN.md` can record its exact immutable commit hash. The follow-up commit is
metadata-only and does not change the reviewed Phase 2 implementation baseline.
