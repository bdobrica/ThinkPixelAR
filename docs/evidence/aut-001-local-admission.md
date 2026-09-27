# AUT-001: bounded standalone admission

Implemented 2026-09-27 in `internal/adapters/authority/local`, through the issuance-only
`authority.Admission` port. This is not full `RunAuthority` lifecycle conformance.

Trusted composition supplies explicit `local` mode, operator policy, a validated
Runtime Profile registry, the existing transactional persistence store, and a UTC
clock. `ApprovedRuntime` is the operator-approved resolver boundary: its caller
must validate digest-pinned manifests, adapter compatibility and profile minimum
requirements before configuring the allowlist. Admission verifies canonical
binding digests and matches the persisted Session to that exact approved binding.
`Caller` must come from authentication and Session authorization, never request JSON.

Profiles must fit positive global CPU, memory, ephemeral/durable storage and process
ceilings, architecture and network allowlists. Omitted requests use the configured
profile and finite default duration; explicit over-ceiling or unknown values fail
closed. Resource requests and limits may only decrease. A network name must match
the selected profile's resolved enforcement policy; changing a string cannot select
new egress. GPU/device execution is disabled in this initial lane. Supported local
capability classes are `shell`, `process`, and `fork`, granted only when explicitly
requested and operator-allowed. These do not authorize enterprise tool side effects.

Grants include local mode/issuer, caller and Session version/generation bindings,
exact runtime evidence, effective profile, original profile/implementation digests,
policy evidence digest and trusted-clock issuance/expiry. The caller's trusted
cutoff can shorten expiry. Operator configuration and registry resolutions are
copied when the authority is constructed.

The existing tenant-scoped idempotency transaction atomically stores issuance and
its replay response. The adapter additionally hashes every admission constraint,
so reusing a claimed request digest cannot conceal changed resources or duration.
Concurrent requests elect one grant; a new adapter/store returns that same snapshot,
even after its expiry or a policy change. Replay does not validate or renew authority.
Denial rolls back the reservation. Retention is maximum duration plus 24 hours;
Execution binding must persist the grant independently before replay records expire.
No migration or process-local grant cache was added.

## Verification

Focused race tests cover defaults, narrowing, finite cutoffs, policy-copy isolation,
identity requirements, invalid configuration, over-ceiling requests and redacted
storage errors. The PostgreSQL test covers eight concurrent admissions, fresh
adapter/store replay after expiry and policy change, conflict with an unchanged
caller digest, and rollback of denied admission. Run against a migrated test database:

```sh
THINKPIXELAR_TEST_DATABASE_URL='<test database URL>' \
  go test -race ./internal/adapters/authority/local -count=1
```

## Remaining composition

AUT-002 owns integrity-bound lifecycle validation, cancellation/expiry and terminal
status. AUT-003 owns API/telemetry visibility. SES-001 and EXE-001–002 must authenticate
and authorize callers, recheck Session version/generation atomically while binding
the grant to one Execution, and supply its effective constraints to materialization.
Issuance alone never permits compute or replaces Attempt fencing. Rate/concurrency
quotas, GPU support and public configuration/API wiring are not implemented here.
No sandbox, live model, AG-governed operation or complete standalone flow was tested.
