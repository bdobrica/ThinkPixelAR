# ADR-0038: Durable agentd bootstrap delivery tracking

Status: Accepted 2026-09-22

## Decision

Implement ADR-0028's publication ordering with a PostgreSQL delivery journal and
bootstrap Delivery coordinator. Store only the planned Reference (credential
metadata, namespace, name and bundle digest), observed provider UID, expiry and
irreversible cleanup status. Keys, raw proofs and bundle bytes never reach this
journal. Migration 0020 adds tenant RLS and immutable identity/history constraints.

SavePlan verifies the complete current registered bootstrap metadata before
creating a single-winner durable claim for that credential's publication. Only
that successful caller may issue a create; repeated/concurrent claims fail.
This deliberately narrows the underlying Secret adapter's create-or-verify API:
a retry after deletion must not recreate a Secret whose cleanup was completed.
An ambiguous publication is recovered for exact cleanup, not blindly recreated.
A caller must not bypass the coordinator to retry publication.

The coordinator requires a current-authority callback as well as the adapter's
registered/unconsumed-credential checks. It rechecks authority before returning
a projection name. The UID is committed before Resolve; any publication/binding
failure records cleanup intent with a separate bounded context. Failure of that
best-effort scheduling cannot erase the already durable expiry cleanup plan.

Cleanup requires no renewed execution authority. It resolves an unknown UID only
from exact ownership/content evidence, persists that UID, then deletes with the
existing provider preconditions before marking completion. A NotFound during
ambiguous creation remains pending: an in-flight create could still arrive.
Absence alone never completes a UID-less plan. A later pass can find and delete
that delayed object. Retained absent plans contain metadata only; operators must
not delete them merely because one lookup found nothing.

Sweep selects at most 128 entries for one explicit tenant, using database time.
Cleanup requests advance a five-second retry timestamp. Ordering by retry time
prevents permanently absent plans from monopolizing every bounded pass. There
is no cluster-wide tenant discovery or unbounded provider list.

## Consequences and remaining composition

This completes the durable delivery coordinator, not AGD-020's executable wiring.
The materialization path must use Delivery.Publish, the accepted-stream/failure
path must request cleanup for its registered credential, and the control-plane
worker must call Sweep periodically for its authorized tenant set. Rotation,
command/replay policies and binary dispatch remain separate AGD-020 work.

Tests cover ordering, failed journal writes, failed UID binding, authority changes,
late provider creation, exact cleanup, real PostgreSQL restart persistence,
concurrent claims, tenant RLS and irreversible cleanup. No migration stores
sensitive material. See [evidence](../evidence/agd-020-bootstrap-delivery.md).
