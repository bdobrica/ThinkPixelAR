# ADR-0040: Local agentd admission and durable dispatch claims

Status: Accepted 2026-09-22

## Decision

Implement ADR-0027's AdmissionPolicy and FramePolicy with a PostgreSQL adapter
for explicitly selected local mode and issuer `thinkpixelar/local`. ThinkPixelAG
mode fails closed; it never falls back to local authority. This adapter validates
already admitted Executions, not initial LocalAuthority admission or grant issuance.
The existing RunAuthority contract continues to govern that later composition.

Trusted AR materialization registers one immutable, non-secret snapshot per
SandboxBinding: validated agentd configuration, handshake challenge, HarnessHandle,
local grant digest/reference, operator revision, acquisition request digest, and
finite authority/bootstrap deadlines. Registration requires the current persisted
Session/Execution/Attempt fence and exact local grant identity. Snapshot deadlines
cannot exceed the persisted compute/Execution deadline. Snapshot registration is
not available to agentd, Hello, a user payload or Workspace content. The registered
configuration must be the one projected by bootstrap materialization.

Every authorization reloads this record, checks its revision against explicitly
configured policy, and matches the current Execution's mode, issuer, grant and
reference. Revocation is irreversible. A revision change invalidates old snapshots;
it never widens them. Existing admission composition independently checks provider
effective facts and credential state before consuming proofs or delivering frames.
Recovery bootstrap is denied for the immutable-projection lane; trusted recovery
must fence and replace the Sandbox. Existing gRPC rate/size/liveness limits remain
mandatory; these policies are not a standalone transport implementation.

## Replay and outcomes

Migration 0021 adds tenant-RLS materialization, command and sequence tables. Frame
authorization locks the current aggregate/binding fence, reloads authority, and
checks the durable connection ID, epoch and deadline. Only registered process-control
commands, bounded candidate output, heartbeat, correlated response, rotation, and
bounded liveness acknowledgement shapes are accepted. Heartbeat progress cannot
exceed sent frames or invent an active operation. Reports never advance lifecycle.

Before the first command is allowed through Send, the same transaction records its
operation ID, digest, message ID, sequence and connection identity as PENDING.
Concurrent claims have one winner. An operation ID is unique within its tenant,
including across sandbox replacements. No second delivery is authorized for an
existing operation, even if the transport frame is identical. Callers reconcile
through the CommandOutcomes port instead of invoking Send again. Thus a crash after
claim and before Send can lose an operation; it cannot authorize double dispatch.
This is at-most-once dispatch, not a claim of exactly-once process effects.

Only a response matching the claimed operation/digest, connection epoch and handle
can finish a claim. Acknowledgements additionally match the original message ID and
sequence. Outcomes are ACKNOWLEDGED or UNKNOWN, with one immutable terminal winner;
identical acknowledgement replay is harmless. ACKNOWLEDGED proves protocol handling,
not execution success. Missing acknowledgements remain PENDING/ambiguous indefinitely.
Both PENDING and UNKNOWN block another command on that sandbox. Recovery requires
trusted fencing/replacement, not minting a new operation key to retry ambiguous work.

The initial lane retains at most 128 commands per sandbox and one outstanding
command, including STATUS. Command history is not deleted or evicted. Per-direction
sequence state retains only the latest frame digest per connection; raw frames,
outputs, credentials and private keys are never persisted. Outcome reads remain
available after cancellation/revocation for reconciliation, without granting work.

## Scope and evidence

[PostgreSQL evidence](../evidence/agd-020-admission-replay.md) covers concurrent
claims, restart reconstruction, fencing, revocation and composition with real
binding/credential stores. Provider observations remain a fixture. This completes
the policy/persistence step, not AGD-020's binary, delivery-worker, shutdown and
replacement-recovery integration. No cross-component responsibility changes.
