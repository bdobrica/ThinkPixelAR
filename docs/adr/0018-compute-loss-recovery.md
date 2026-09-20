# ADR-0018: Record compute loss and stale-resource recovery atomically

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Missing or unverified bound compute must not silently terminate a durable Session.
An old binding may also outlive its Attempt, cancellation or execution deadline.
Cleanup must remain exact and restart-safe without granting execution authority.

## Decision

When a fenced observation requires recovery, atomically record binding uncertainty,
one `sandbox.recover` work item, a bounded ordered event and its versioned outbox
envelope. For the still-current materializing/running Attempt, move ACTIVE to
DEGRADED while preserving its current Execution and generation. This blocks
Attempt mutations under the existing ACTIVE-only fence. Do not terminalize or
replace an Execution, guess an external outcome, or admit another Attempt.

Stale bindings are treated as orphaned owned compute. They create recovery/cleanup
work without modifying a newer Attempt/Execution or an ongoing cancellation
workflow. A saved exact provider UID permits a durable cleanup intent and release
journal entry in the same transaction. Without that identity, retain recovery work
for ambiguity resolution and do not guess a deletion target. A cleanup ownership
or integrity failure quarantines cleanup; it never broadens deletion scope.

A committed exact cleanup intent can be loaded even if AR crashed before the
provider journal call. Reuse its operation identity. Compute monitoring performs
release, while separate recovery work remains pending for domain recovery policy.

## Alternatives considered

Terminalizing from Pod loss confuses replaceable compute with durable Session
continuity. Recreating the same bound identity hides loss and may replay ambiguous
side effects. Cleaning by name/label alone risks deleting another resource.
Automatically retrying an Execution bypasses authority and side-effect analysis.

## Consequences

Migration 18 corrects the older Session epoch trigger to allow the normative
DEGRADED/recovery-state-ACTIVE association with the unchanged current Execution.
The immutable old migrations are preserved. Attempt mutation still requires
ACTIVE; the change does not authorize work while degraded.

Recovery handling and safe Attempt replacement remain later application workflow
work. This item detects missing and stale **persisted bindings** through their
monitoring jobs. It does not claim a cluster-wide inventory of resources that never
had an AR binding; unknown resources are not deletion candidates. Broader orphan
inventory remains operational hardening work.

## Security

All effects share the tenant-scoped fenced observation transaction. A stale
observation cannot degrade a newer Attempt. Provider API failures remain retryable
uncertainty and do not trigger absence recovery. Cleanup retains the original
acquisition ownership digest and exact UID reference; quarantine stops authorization
of further deletion. Events contain closed reason codes and stable AR IDs only.

## Operations

Inspect pending `sandbox.recover` work and quarantined cleanup. Confirm authority,
Workspace and external-action outcomes before recovery. Physical cleanup completing
does not complete the recovery job or make the Session ready. No additional
infrastructure is required.

## Compatibility

Preserves published Session semantics and wire schemas. Uses existing work,
event, outbox and cleanup tables; only the corrective trigger migration is new.

## References

- [Session state machine](../contracts/session-state-machine.md)
- [Session fencing](../contracts/session-single-writer-fencing.md)
- [KAS-018 evidence](../evidence/kas-018-compute-recovery.md)
