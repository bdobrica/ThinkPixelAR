# Suspend and resume

Status: Normative Phase 0 contract.

## Semantics

Suspend converts a quiesced Session from live compute to fully durable, independently restorable state. Resume validates that state and reconstructs fresh compute; it does not revive a Pod, process, socket, lease, or credential. The semantic boundary is the committed Checkpoint, not the survival of a particular Sandbox.

```mermaid
sequenceDiagram
    participant AR as Agent Runtime
    participant H as Harness/agentd
    participant W as WorkspaceProvider
    participant DB as Durable state
    participant S as Sandbox provider
    AR->>H: Fence new work and quiesce/export
    AR->>W: Snapshot durable Workspace
    AR->>DB: Commit generation + Checkpoint + SUSPENDED + release intent
    DB-->>AR: Durable success
    AR->>S: Revoke/release compute
    Note over AR,S: Cleanup may retry; Session is logically SUSPENDED
    AR->>DB: Validate resume request and checkpoint
    AR->>S: Create fresh Sandbox/Attempt binding
    AR->>W: Attach/restore exact generation
    AR->>H: Restore vendor state, no Execution authority
    AR->>DB: Commit READY/IDLE and cleanup old binding
```

## Minimum durable release state

Before AR may intentionally release the last usable compute for a Session, one transaction must durably bind:

- Session identity, tenant, state `SUSPENDED`, optimistic state version, current execution generation, and suspend operation;
- a `COMMITTED` [Checkpoint](checkpoint.md), including compatible runtime/adapter/protocol/state format and immutable vendor-state references;
- the Checkpoint's committed [WorkspaceGeneration](workspace.md), ready provider snapshot/export reference, integrity root, and retention ownership;
- immutable AgentRuntimeSpec and resolved RuntimeProfile/config digests needed to recreate the Sandbox;
- source/materialization and lineage references needed for audit, not for refetching mutable content;
- the exact SandboxBinding/Attempt/attachment being retired, credential-revocation boundary, and idempotent compute/storage cleanup intents;
- audit/event/outbox evidence and retention/deletion disposition.

An active/running Execution is not resumable state. Before the transaction, any Execution is terminalized or otherwise completed under its defined lifecycle with terminal response/outbox facts durable. Pending user/tool input, in-flight side effects, streaming offsets, approvals, and delivery ambiguity must be represented by their owning durable protocols or make suspend fail; they cannot live only in process memory.

Excluded state includes all credentials/refresh material, bootstrap data, gateway tokens, provider handles used as authority, sockets, open file descriptors, process IDs/memory, network connections, locks/leases, Kubernetes object liveness, node-local caches, and writable image/scratch contents.

## Suspend protocol

Suspend is an authorized, idempotent Session mutation with `operation_id` and canonical request digest.

1. Verify tenant/Session/state/version, no conflicting lifecycle operation, and policy/retention capacity. Reserve `SUSPENDING` and reject new Executions.
2. Fence the current Execution/Attempt generation and stop new harness/tool/model work. Resolve any in-flight operation to a durable terminal/ambiguous protocol outcome.
3. Ask HarnessAdapter to quiesce and export bounded immutable vendor state; stop processes if quiescence requires it.
4. Flush/snapshot Workspace and atomically publish the next generation and signed Checkpoint as specified by their contracts.
5. In the same authoritative transaction set Session `SUSPENDED`, link the Checkpoint/generation, append event/outbox/evidence, record retention, revoke the binding/credential generation, and create exact detach/release/secret-cleanup intents.
6. Only after commit acknowledge suspend and detach/release the Sandbox idempotently. Cleanup failure does not roll the Session back to live; it is visible reconciliation work and stale compute remains fenced.

If any requirement through step 5 fails, suspend is not successful and compute is not intentionally released. Policy may replace failed compute only through the normal recovery path, never by pretending an incomplete checkpoint is resumable.

## Resume protocol

Resume is an authorized, idempotent Session mutation from `SUSPENDED` using the Session's exact committed Checkpoint by default. Choosing an older retained Checkpoint is an explicit restore operation with expected state version and lineage evidence.

1. Validate caller/tenant/Session, state/version, operation replay, retention, Checkpoint signature/object integrity, Workspace snapshot, and exact compatibility matrix.
2. Re-resolve the immutable AgentRuntimeSpec/RuntimeProfile without widening capabilities, network, resources, tools, or mounts. Revoked/disabled specs, profiles, adapters, keys, or policies fail closed or require a separate governed migration.
3. Reserve `RESUMING`, a new SandboxBinding and Attempt/generation; create fresh compute/bootstrap identity.
4. Restore/attach the Checkpoint's WorkspaceGeneration as the Session's single writable Workspace head and verify effective storage facts/integrity.
5. Bootstrap `agentd`, negotiate the recorded-compatible adapter, restore vendor state, and perform health/semantic readiness checks. No prior Execution credential is restored or issued during this infrastructure step.
6. Transactionally publish Session `IDLE`/`READY`, current Workspace/attachment and fresh Sandbox binding; append event/outbox/evidence and cleanup intents for abandoned candidates.
7. A later accepted Execution receives a fresh ExecutionGrant/credential bound to its own identity, Attempt, generation, audience, scope, and deadline.

Until step 6, external APIs observe `RESUMING`; the candidate Sandbox cannot accept user work. Resume never mutates the immutable Checkpoint. If the provider restores via clone, AR records a new WorkspaceGeneration lineage while preserving the logical resume boundary.

## Concurrency and idempotency

Only one suspend, resume, close, restore, or fork lifecycle operation may hold the Session transition lease/version at a time. Same operation/digest returns the same Checkpoint or resulting binding; same ID/different digest conflicts. Execution admission is rejected during `SUSPENDING`, `SUSPENDED`, and `RESUMING`.

Provider timeouts are ambiguous. AR reconciles exact persisted references and ownership rather than allocating duplicate Workspaces/Sandboxes. Candidate resources are never promoted if Session version, binding, Attempt, or generation changed. A resumed Session has one current writer and one current Sandbox maximum.

## Failure and recovery

| Condition | Required behavior |
| --- | --- |
| Quiesce/export/snapshot/sign/commit fails | Suspend fails; do not intentionally release compute or expose a partial Checkpoint. |
| Suspend commit succeeds, response lost | Replay returns the same suspended state/Checkpoint; cleanup continues. |
| Compute release/detach fails | Session remains logically `SUSPENDED`; revoke/fence stale authority and retry exact cleanup. |
| Old Sandbox reconnects | Reject by revoked binding/Attempt/generation; no commands, credentials, or Workspace mutation. |
| Resume allocation/attach/bootstrap fails | Keep Session `SUSPENDED` (or resumable degraded state), clean candidate exactly, retry same operation safely. |
| Integrity/compatibility/policy validation fails | Do not allocate or start harness; return deterministic degraded/incompatible reason. |
| Resume commit succeeds, response lost | Reconcile and return the one committed fresh binding; discard losing candidates. |
| AR crashes at any boundary | Durable operation/intent drives reconciliation; process memory is never required. |

## Security invariants

- Suspend/checkpoint identifiers confer no resume authority; every mutation reauthorizes tenant, Session, state, and policy.
- All old Execution/bootstrap/gateway credentials are expired/revoked/fenced before release; none are checkpointed.
- Resume cannot widen immutable runtime resolution, network, mount, tool, model, resource, or retention policy implicitly.
- Stale compute cannot write the Workspace, publish events, refresh credentials, or complete an Execution.
- Vendor/Workspace content remains untrusted and tenant-bound throughout snapshot, storage, restore, and cleanup.
- Errors/evidence contain bounded identities/digests/reasons, never secrets or restored content.

## Verification requirements

- Crash and response-loss injection before/after every numbered suspend/resume step, including database transaction boundaries.
- Full node/Sandbox/AR replacement proving restoration from only the documented durable minimum plus fresh authority.
- Active Execution, stream, tool/model ambiguity, approval, cancellation, timeout, and concurrent close/fork/execute races.
- Stale Sandbox/Attempt reconnect and write tests after successful logical suspend and during resume.
- Missing/corrupt/substituted Workspace/vendor objects, invalid signatures, revoked runtime/profile/key, and compatibility failures.
- Duplicate operations and candidate allocations proving one Checkpoint, one writer, one committed binding, and exact cleanup.
- Credential canaries proving old authority is absent and unusable after both suspend and resume.
