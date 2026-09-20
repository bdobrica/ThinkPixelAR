# ADR-0016: Reconcile compute without transferring Session authority

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

A provider response can be delayed, lost or stale. Infrastructure readiness cannot
establish a valid ExecutionGrant, complete an Execution, or decide that a durable
Session has ended. The initial profiles use cold acquisition and release-and-restore.

## Decision

Use a small application reconciler with explicit authoritative intent/observation,
compute-provider and current-authority ports. Load persisted binding, desired
compute state, original operation identity, aggregate-current status and exact
cleanup authorization. Check current authority before reconciling running compute.
Require exact cleanup authorization for release, without renewing expired authority.

Reconcile one bounded observation/action per call, outside database transactions.
Retry the original acquisition only when the saved binding has no provider
reference. Missing previously bound compute requires recovery, never a fresh
SandboxID or silent Session terminalization. Treat API outage/UNKNOWN as uncertainty.
Require verified effective facts before reporting infrastructure convergence.
Release acceptance remains pending until a subsequent provider read proves absence.

Record results through a port that rechecks aggregate and operation fences and
compare-and-swaps the binding version. A stale result has no authoritative effect.
Use a closed set of application-generated reason codes; never persist provider
error text. Recovery-required outcomes are explicit inputs to durable recovery
work, not permission for immediate retry of an ambiguous agent action.

## Alternatives considered

A long transaction across Kubernetes calls blocks unrelated authoritative work
and still cannot make the external operation atomic. Treating all errors as
NOT_FOUND duplicates compute. Marking a Session terminal from Pod failure destroys
its durable continuity. Automatically resuming a suspended process bypasses the
Session checkpoint/fresh-authority protocol.

## Consequences

The initial reconciler supports cold running compute and exact release. Native
provider Suspend/Resume remain adapter capabilities, not automatic Session
continuity. Full Session suspend/restore and harness startup belong to their later
application workflows. PostgreSQL intent loading, durable worker scheduling and
recovery effects are implemented separately behind these ports (KAS-017/018).

## Security

Provider readiness and worker leases never replace grant/Attempt/generation
fences. Cleanup must identify the exact resource and operation. Stale, cross-tenant
or unverified results cannot advance execution. Existing active compute is not
stopped by a failed observation alone: durable recovery must fence it and record
cleanup/revocation work.

## Operations

Call the reconciler from leased durable work, with bounded provider timeouts and
retry schedules. Inspect sanitized observation codes. Supply real authority and
persistence adapters; a synthetic qualifier is only a test fixture.

## Compatibility

Kubernetes types remain inside infrastructure adapters. No public API, wire schema
or Session state machine changes. The compute-provider port is the lifecycle
subset needed by this reconciler, not a claim of complete provider capabilities.

## References

- [SandboxProvider](../contracts/sandbox-provider.md)
- [Session fencing](../contracts/session-single-writer-fencing.md)
- [Suspend/resume](../contracts/suspend-resume.md)
