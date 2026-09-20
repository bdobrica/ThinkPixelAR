# ADR-0006: Implement against Agent Sandbox v1.0.0 beta APIs

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: ADR-0005 dependency patch selection only
- Superseded by: None

## Context

The Phase 0 cross-system review marked v0.5.5 superseded and required selecting
v1.0.0 or justifying another maintained line. KAS-002's reference to the Phase 0
pin must include that follow-up, rather than revive the historical candidate.

## Decision

Pin `sigs.k8s.io/agent-sandbox` to `v1.0.0`, source commit
`bb72f49d79f009a960eed2ae6c32e1cc082399c5`, with core and extension `v1beta1`
APIs. Import only API packages within the Agent Sandbox adapter. Do not embed
upstream controllers or routers. Register Sandbox, SandboxTemplate and
SandboxClaim through the upstream scheme; reject legacy alpha APIs.

The upstream module requires Kubernetes modules `v0.36.4`; use that exact patch
instead of ADR-0005's initial `v0.36.2` dependency. This changes the source
qualification target to Kubernetes 1.36.4, without claiming the homelab K3s
packaging is equivalent to an upstream Kubernetes qualification lane.

## Alternatives considered

Retaining v0.5.5 contradicts the recorded Phase 0 follow-up. Vendoring modified
upstream types or copying CRD structs would obscure compatibility and drift.

## Consequences

The module graph grows, while compiled imports remain limited to API and client
support. An AST-based test rejects Kubernetes and Agent Sandbox imports outside
infrastructure adapters. Public contracts remain neutral and unchanged.

## Security

The pin does not qualify runtime isolation. Readiness must inspect effective
Pod facts; native labels and condition messages cannot confer authority. The
upstream default network policy is broader than secure AR profiles and must
not be used as an implicit egress policy.

## Operations

No controller is installed by this library change. Install exact CRDs/controller
artifacts separately and prove lifecycle/network/storage behavior. Existing
v0.5 deployments require upstream stored-version migration before upgrade.

## Compatibility

v1 removes alpha APIs. Operating mode is desired state; boolean conditions are
observations. Explicitly disable inbound Service creation for the ADR-0002
outbound transport. Warm pools remain unqualified and disabled. `PH0-KAS-001`
remains open until live qualification evidence passes.

## References

- [Phase 0 review](../evidence/phase-0-cross-system-review.md)
- [SandboxProvider contract](../contracts/sandbox-provider.md)
- [Upstream v1.0.0 source](https://github.com/kubernetes-sigs/agent-sandbox/tree/v1.0.0)
