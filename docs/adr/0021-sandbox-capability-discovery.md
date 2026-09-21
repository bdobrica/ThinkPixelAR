# ADR-0021: Sandbox capability discovery

Status: Accepted 2026-09-21

## Context

The SandboxProvider contract requires discovery before accepting a profile.
Operator version documentation alone cannot reject an unavailable API, changed
CRD or incomplete controller rollout. Capability vocabulary describes implemented
operations, not physical isolation or execution authority.

## Decision

The Agent Sandbox adapter requires a trusted capability resolver and expected
digest. The Kubernetes resolver reads the exact pinned server version, required
core/beta resource scopes and verbs, established beta-only CRD storage and the
pinned controller image with a completed Deployment rollout. Each resolution has
a ten-second deadline; discovery documents are limited to one MiB. No cached
observation becomes admission authority.

The CRD spec hash is derived from the reviewed, digest-verified installation
manifest through a server dry-run to include API defaults. It is never learned
from the currently installed spec. Operator credentials remain outside workloads.
The resolved capability digest includes the installation pin, CRD UID, controller
UID/generation and supported vocabulary. Reinstallation or changed configuration
requires deliberate re-resolution and a new immutable implementation snapshot.

Acquisition checks discovery and profile compatibility before reservation or
external mutation. Native suspend/resume checks precede durable operation intent;
secure READY also requires a fresh match. The expected digest is included in the
immutable coding-template configuration and trusted Pod-template annotation.
Effective verification compares that marker; a workload's marker alone is never
proof. Read/reconciliation and exact-UID release remain possible during discovery
failure, while secure READY is withheld.

Supported vocabulary includes native suspend/resume, Linux amd64/arm64, the
implemented network classes and single-writer attachments. It excludes GPU and
warm pools. Runtime artifacts, physical enforcement, Workspace storage and fresh
execution authority retain their separate mandatory verification boundaries.

## Consequences

Admission depends on bounded live Kubernetes reads and fails closed on drift or
outage. The trusted control plane needs GET access to discovery, the core CRD and
the selected controller Deployment; this gives no new permission to sandboxes.
The homelab pin is a reproducible installation input, not production qualification.
No public API, ThinkPixel integration boundary or external authority changes.
Full admission service composition remains Phase 6 work.

See the [runbook](../operations/agent-sandbox.md),
[SandboxProvider contract](../contracts/sandbox-provider.md), and
[closure evidence](../phase-3-evidence.md).
