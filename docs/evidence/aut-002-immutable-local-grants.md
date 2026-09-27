# AUT-002: immutable local grants

Implemented 2026-09-27. Admission atomically writes a tenant-scoped immutable
snapshot, its SHA-256 digest, initial `ACTIVE` status, and the existing replay
response. Migration 22 adds independent grant storage: idempotency cleanup cannot
remove cancellation evidence. PostgreSQL rejects snapshot/digest/identity changes,
status reversal, and deletion; the repository exposes no snapshot update method.

`authority.LocalLifecycle` provides `Validate` and `Cancel`. Both require trusted
caller authentication and Session authorization, verify the issuing principal,
tenant, local issuer/mode, and exact stored snapshot, and use the authority clock.
The row is locked before reading the clock. Expiry is inclusive at `expires_at`;
cancellation/expiry use versioned updates and one irreversible terminal winner.
Cancellation is idempotent. Validation returns a separate status, never a modified
or renewed grant. Errors fail closed with safe codes. Configuration changes cannot
reinterpret or widen a previously issued snapshot.

Returned Go values remain ordinary transport copies. Editing one cannot change
the durable record or pass validation. Admission replay still returns the original
snapshot after cancellation/expiry and is never proof of active authority.

## Verification

Ran focused race tests, migration loader tests, and `go vet` on affected authority,
persistence and PostgreSQL packages. Applied all migrations to an isolated local
PostgreSQL database and ran:

```sh
THINKPIXELAR_TEST_DATABASE_URL='<migrated test database URL>' \
  go test -race ./internal/adapters/authority/local -count=1
```

Integration coverage includes admission replay, mutated nested resources/runtime/
network/capability/policy evidence, wrong tenant/principal/Session/generation,
unknown grant, pre-issuance time, exact expiry boundary, concurrent cancellation
and expiry, fresh adapter/store reconstruction, config changes, replay retention,
and direct SQL attempts to rewrite issuance bytes/digest. No live sandbox or model
was exercised.

## Integration limits

This is the AUT-002 immutability/cancellation/expiry slice, not full RunAuthority
conformance. Heartbeat/Attempt validation, Complete/Fail terminal reporting,
revision revocation, and API/Execution composition remain. Execution admission must
atomically bind the exact grant to one Execution; callers must compare that binding
and current Session generation/Attempt fences before forward work. Cancellation
does not itself stop compute. No credentials or enterprise authority are added.

Apply migration 22 before using this adapter. Pre-AUT-002 replay-only grants have no
independent authority record and fail validation; replay does not backfill or revive
them. Issue a fresh operation instead. Grant history is retained; a future authorized
retention mechanism must preserve terminal evidence for all referenced Executions.
