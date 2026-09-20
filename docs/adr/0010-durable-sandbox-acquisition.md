# ADR-0010: Persist cold acquisition and an ordered lifecycle journal

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Provider calls may succeed despite a lost response. Process memory cannot own the
mapping between an AR Attempt and disposable compute. The original binding schema
preserves one immutable operation identity per lifecycle kind, but repeated
suspend/resume cycles require a retained operation history.

The pinned Agent Sandbox v1.0.0 SandboxClaim API requires `warmPoolRef`. The secure
profile and ADR-0006 disable warm pools until residue and identity qualification.
There is no direct template-reference claim acquisition in this pinned API.

## Decision

Use direct cold Sandbox acquisition for the initial profile. Do not introduce a
nominal zero-size warm pool or a claim controller dependency merely to wrap cold
creation. SandboxClaim acquisition is inappropriate for this profile; enabling
it later requires warm-pool qualification and a separate reviewed implementation.

Reserve the neutral acquisition request in PostgreSQL before external mutation.
Check current Session/Execution/Attempt fences, deadline, immutable Session runtime
and saved Execution resolution. Save canonical request bytes and bind the exact
opaque provider UID reference once. Retain ambiguous results for reconciliation
and cleanup, even when the Attempt has since been fenced.

Use UUIDv7 operation identities, consistent with existing AR persistence/domain
identity rules. Serialize operation keys and binding mutations; append immutable
lifecycle operations with increasing revisions. Replaying a superseded operation
fails. Release is final for that physical binding. Preserve the existing first
operation columns rather than rewriting their meaning or old migrations.

Current execution ownership permits lifecycle changes. After fencing/cancellation,
release requires a matching durable cleanup intent: tenant, owner, provider,
exact external reference, operation identity/digest and acquisition ownership
proof. Cleanup never reauthorizes execution.

## Alternatives considered

An in-memory cache loses ambiguous acquisition on restart. Replacing a binding
UID can adopt unrelated compute. Overwriting the original lifecycle identity
columns destroys history. Claim-backed pooling is deferred because neither pool
adoption nor residue safety is currently qualified.

## Consequences

Two forward migrations add canonical request/journal tables and repair the
existing polymorphic binding trigger's field dispatch. A Boolean SQL conjunction
cannot safely protect references to fields absent from the current record type;
PL/pgSQL now branches by table before preparing those expressions.

The store uses AR-owned tables only and returns bounded errors. Application
composition, reconciliation and network/storage admission remain separate work.
Old bindings without replay records are not reconstructed from Kubernetes labels;
they require explicit investigation/recovery.

## Security

Forced row-level security applies to both new tables. Explicit tenant predicates
remain defense in depth. Requests contain opaque bootstrap references, never
credential values. Mutation does not widen the saved profile/runtime or deadline.
RunAuthority validation remains an application responsibility.

## Operations

Apply migrations before using the store. Preserve old ledgers/checksums. The
journal supports repeated cycles and restart replay; do not delete reservations
because an API call timed out. Monitor errors without logging request payloads.

## Compatibility

Public wire contracts remain unchanged. Kubernetes types stay within adapters.
Existing binding columns, identity triggers and foreign keys are retained. The
new store rejects non-UUIDv7 operation IDs, matching the existing domain model.

## References

- [SandboxProvider](../contracts/sandbox-provider.md)
- [ADR-0006](0006-agent-sandbox-v1-api-pin.md)
- [Pinned claim API](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.0/extensions/api/v1beta1/sandboxclaim_types.go)
- [KAS-010 evidence](../evidence/kas-010-bindings.md)
