# ADR-0009: Render coding templates from immutable qualified configuration

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

The coding profile describes abstract guarantees. Agent Sandbox requires concrete
Pod settings, but those types and choices must remain within infrastructure
adapters. Profile names, desired manifests and runtime branding are not physical
qualification evidence.

## Decision

Build an immutable coding-template mapper from schema-validated profile JSON,
operator configuration and a mandatory trusted qualification callback. Include
all concrete mapping inputs and the qualification-evidence digest in the
canonical implementation snapshot. Reject unsupported warm pools, GPU,
confidential isolation and seccomp modes rather than guessing implementations.

Rendering requires exact profile and implementation digests plus the original
profile values. It maps the immutable runtime image/entrypoint, architecture,
resource requests/limits, non-root identities, restrictive security context and
bounded temporary volume. Workspace, vendor state and bootstrap references resolve
to existing resources; the renderer never provisions storage or reads credentials.
Vendor state and Workspace use distinct claims. Bootstrap is a read-only Secret
mount, never an environment variable or service-account token.

Disable inbound Services, claim environment injection and PVC-template injection.
Set an explicit empty managed template NetworkPolicy to avoid upstream's implicit
public-Internet egress. Direct Sandbox creation must use the separate AR network
enforcer before creating compute; template policy fields alone do not apply to
that path. Only trusted network enforcement can add the exact approved exceptions.

## Alternatives considered

Arbitrary Pod YAML would create an excessive operator surface and permit silent
security drift. Rendering from the currently reloaded profile would rewrite old
execution intent. Upstream defaults do not satisfy the secure AR network contract.

## Consequences

Pure rendering is deterministic and independently testable. It provides neither
Run authority nor effective-state proof. Production qualification, attachment
resolution, network application and live gates remain required downstream work.
No template is deployed by constructing or rendering the mapper.

## Security

No privileged containers, host namespaces, hostPath, runtime socket or API token
are rendered. Drop all capabilities, require runtime-default seccomp and a
read-only root. Hard limits and fixed mount roots come from the saved profile.
The process bound is an external node/runtime qualification requirement because
PodSpec has no portable per-Pod process-limit field.

## Operations

Keep each mapper for the lifetime of its saved resolution. Return copies of all
snapshots and manifests. Configuration changes create a new implementation digest;
existing requests fail on substitution. Node/runtime/storage/network qualification
must reject unsupported profiles before admission.

## Compatibility

The public profile schema is unchanged. Concrete mappings remain adapter-local.
The initial mapping supports the existing non-GPU Linux microvm-strong coding
profile; it does not silently shrink its resources for the ARM64 homelab.

## References

- [Runtime Profiles](../contracts/runtime-profiles.md)
- [SandboxProvider](../contracts/sandbox-provider.md)
- [KAS-009 evidence](../evidence/kas-009-template.md)
