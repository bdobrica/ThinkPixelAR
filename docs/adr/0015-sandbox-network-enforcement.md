# ADR-0015: Gate sandbox compute on qualified namespace networking

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Network policy must protect a workload before it starts. Kubernetes policies are
additive, so checking a single named default-deny policy cannot establish the
absence of broader allowances. Manifest inspection alone does not prove CNI,
protected-destination or service identity enforcement.

## Decision

Require an adapter-local network enforcement hook for strong-isolation acquisition,
resume and READY observations. Acquire reserves durable identity before invoking
the hook, and invokes it before any Kubernetes Sandbox create. Missing enforcement
fails before reservation. Failures retain the reservation for exact replay.

Provide a narrow implementation for a dedicated operator-controlled namespace
with exactly one preinstalled NetworkPolicy. Bind its namespace, name, UID and
complete spec to immutable profile and implementation digests. Check the complete
policy list, rejecting additional policies, pagination, replacement, deletion and
spec drift. Select every Pod, deny ingress, and require both policy directions.
Allow only bounded numeric TCP/UDP ports to exact namespaces and nonempty Pod
label selectors. Do not translate FQDNs or identities into broad IP blocks.

Require a separate trusted qualification callback on every check. It proves the
selected network class permits each peer, actual peer/service identity, namespace
ownership and CNI enforcement, including metadata/API/node bypass prevention.
For `none`, only required DNS and AR transport are eligible. Other bounded classes
may use qualified gateway/mirror peers; unrestricted networking is unsupported.
Manifests alone never satisfy this callback.

## Alternatives considered

Workload-selected exceptions expand authority. A policy selecting only labels
added later leaves a startup race. Checking only one named policy misses additive
allowances. Automatically replacing unknown policies risks widening other
workloads or destroying operator state.

## Consequences

One network class/resolution per provider namespace is deliberately restrictive.
Operators install policy before admitting workloads and control namespace policy
and peer labels. The hook supports future enforcers without changing domain types.
This implementation verifies rather than installs shared policy. Release retains
policy, protecting other workloads and avoiding teardown races.

## Security

Exact API observations are a drift check, not atomic physical enforcement. Trusted
qualification must establish ongoing enforcement and prevent unauthorized policy
or destination-label mutation. A failed check blocks create/resume/readiness;
application reconciliation must fence any existing workload on policy drift.
The later reconciler owns that response; this hook does not claim to revoke
credentials or stop an already running Pod. No policy allowance grants application
authority, and transport/gateway authentication remains mandatory.

## Operations

Use dedicated namespaces and operator-only policy RBAC. Correct drift before
retrying the saved operation. Record live denial evidence separately from unit
tests. Homelab deployment uses the same checks without requiring paid infrastructure.

## Compatibility

No public contracts or Kubernetes-neutral domain fields change. Secure adapter
composition now requires a network enforcer as well as an effective-state verifier.

## References

- [Network profiles](../contracts/network-profiles.md)
- [Effective security gate](0013-effective-sandbox-security.md)
- [KAS-014 evidence](../evidence/kas-014-network-hooks.md)
