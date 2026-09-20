# ADR-0012: Resolve Kata through exact operator-controlled RuntimeClass mappings

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

An abstract microvm-strong profile cannot select arbitrary runtime handlers or
node labels. The installed homelab is ARM64, while the initial coding profile
requires amd64. A RuntimeClass object alone is not evidence of hardware isolation.

## Decision

Resolve the profile's opaque isolation-runtime reference through immutable
operator mappings pinned to Kata 3.31.0. Validate the exact observed RuntimeClass
handler, UID, node selector, lack of additional tolerations and CPU/memory
overhead. Reject missing references, unsupported architectures, weakened classes
and drift; never fall back to runc or another handler.

Require a trusted qualification function to validate host/guest evidence for the
exact runtime/artifact/node/architecture/resource tuple. Include its evidence
digest, the complete mapping and observed RuntimeClass UID in canonical resolution
evidence. Desired metadata or labels cannot implement that qualification function.
Return independent copies so callers cannot mutate future resolution.

The checked-in homelab operator mapping selects the installed suffixed handler
and explicitly advertises ARM64 only. Its qualification reference remains a
candidate: it does not qualify the initial amd64 profile or observed overhead.

## Alternatives considered

Passing RuntimeClass names through user/runtime inputs violates the operator
boundary. Selecting by a Kata-looking name mistakes branding for evidence.
Silently substituting ARM64 or reducing limits changes the saved runtime/profile.

## Consequences

Runtime mapping is implementable independently of physical qualification. It
must be composed with storage/network and full-profile admission validation;
runtime resolution alone does not qualify the entire Runtime Profile.

## Security

Recreated RuntimeClasses change resolution identity. Selector or overhead drift
fails closed. Concrete labels/handlers remain in adapters and operator files.
No mapping extends Run authority, attaches storage or creates compute.

## Operations

Update mappings only with reviewed qualification evidence. Existing executions
retain their saved resolution. Measure actual Kata overhead before qualification;
the installer default is recorded, not certified. Unsupported deployments fail
profile validation until a matching tested lane exists.

## Compatibility

Public profile references and schema are unchanged. The mapping is specific to
the pinned Kata implementation; future runtimes need their own qualified adapter.
The ARM64 homelab does not change the supported amd64 production target.

## References

- [Operator mapping](../../deploy/kata/runtime-mapping.json)
- [Kata installation](../operations/kata.md)
- [KAS-012 evidence](../evidence/kas-012-runtimeclass.md)
