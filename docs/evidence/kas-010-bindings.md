# KAS-010 — Durable cold acquisition binding

The v1.0.0 claim API requires a warm-pool reference. The initial secure profile
forbids unqualified pooling, so direct Sandbox acquisition remains selected;
[ADR-0010](../adr/0010-durable-sandbox-acquisition.md) records this applicability
decision. No warm-pool or claim capability is advertised by this change.

`postgres.SandboxBindings` implements durable reservation, lookup, immutable UID
binding and ordered lifecycle operations. The full neutral request survives
process restart. Matching saved runtime/profile/deadline and current generation /
Attempt are mandatory. Release after cancellation requires the exact cleanup
intent. Provider and persistence share the same canonical operation digests.

Real PostgreSQL 18.6 race tests cover six concurrent identical acquisitions,
reconstruction through a fresh store, conflicting request/runtime/UID, repeated
suspend/resume cycles, superseded replay, immutable journals, cancellation fences,
cleanup-intent requirements and release replay. A non-owner, non-superuser role
proves the RLS boundary of all three binding/journal tables. Migration tests cover
an empty schema and upgrade/replay from the original 15-migration Phase 2 schema.

The live tests exposed the old binding trigger referencing fields from the other
binding table. Migration 17 repairs dispatch; migration 9 remains unchanged.

Validation: focused tests, full PostgreSQL adapter/migration race suite and
`make verify`. The retained default local database has a pre-existing migration-3
checksum mismatch; it was not reset or repaired. Tests used a separate
`thinkpixelar_kas010` database in the repository's pinned PostgreSQL service.
No homelab data or workloads were modified for these database checks.

Production service composition, attachment/bootstrap qualification and the live
provider/recovery gates remain downstream work. This is not a Phase 3 exit claim.
