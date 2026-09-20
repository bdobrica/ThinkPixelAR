# ADR-0013: Enforce secure compute before creation and before readiness

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

An unsafe Pod can execute before AR reports READY. Desired security settings can
also drift through admission or replacement. Secure execution therefore needs
both pre-creation constraints and independently observed effective-state checks.

## Decision

Reject unsafe microvm-strong blueprints before durable sandbox reservation or any
Kubernetes create. Require non-root identities, dropped capabilities, runtime-default
seccomp, read-only root, no privilege escalation, no API token or host namespaces,
bounded exact resources and four fixed Workspace/state/tmp/bootstrap mounts.
Reject unreviewed containers, credentials, host storage, subPaths and devices.
The initial coding mapper supports trusted-only DNS; unsupported DNS modes fail
construction rather than inherit a cluster default.

Before READY, inspect the owned actual Pod against the saved blueprint. Compare
all workload-container fields, security context, volumes, resource requests/limits,
runtime selection, selectors, service account, DNS and ownership labels. Allow
only explicitly normalized harmless Kubernetes termination-message defaults.
Reject injected init/ephemeral containers, token/host projections, runtime override
or alternate-network annotations and incomplete running-container evidence.

Require a separate trusted infrastructure verifier for actual guest/handler,
image manifest identity, process/cgroup/resource limits, network enforcement and
mounted/fenced Workspace. A PodSpec, Ready condition or sandbox report cannot
supply that proof. No verifier or uncertain evidence means no secure readiness.

## Alternatives considered

READY-only checks leave unsafe startup exposure. Comparing desired Sandbox specs
alone misses Pod admission drift. Trusting guest-reported security would put the
compromised workload in charge of its own qualification.

## Consequences

The guard is stricter than Kubernetes restricted admission and independent of
whether a caller remembered to install a READY verifier. Legitimate new workload
features require explicit mapping/verification support. Physical qualification
remains separately evidenced, including the ARM64 validation scope selected by
the user and the documented production infrastructure requirements.

## Security

Bootstrap remains the single dedicated read-only credential mount. No implicit
service-account, environment-secret, runtime-socket, hostPath or device path is
accepted. Unknown observed state fails closed without logging provider payloads.
Confidential isolation remains unsupported. Qualified infrastructure evidence is
required even when all manifest checks pass.

## Operations

Admission mutation that changes security-sensitive settings prevents readiness.
Investigate and correct drift rather than widening comparisons. Production image
verification must handle pinned OCI indexes and their approved platform manifests;
an arbitrary observed image ID is not proof of the requested image.

## Compatibility

No public schema changes. Existing provider lifecycle tests now use hardened
blueprints. Container-standard support is not promoted to microvm-strong by these
checks; the secure mapping remains explicitly qualified.

## References

- [Runtime Profile invariants](../contracts/runtime-profiles.md)
- [SandboxProvider](../contracts/sandbox-provider.md)
- [KAS-013 evidence](../evidence/kas-013-security.md)
