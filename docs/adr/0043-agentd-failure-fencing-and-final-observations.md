# ADR-0043: Agentd failure fencing and final observations

Status: Accepted 2026-09-22

## Decision

The hosted immutable-bootstrap lane uses credential expiry as a trusted recovery
trigger. For each explicitly configured binding, AR rechecks the **latest** saved
certificate and connection deadline using database time. Under the same aggregate
and binding locks as issuance/admission, expiry records UNKNOWN compute, degrades
the still-current Session and records the existing recovery event, outbox entry,
`sandbox.recover` work and exact provider cleanup intent atomically (ADR-0018).
A transient disconnect, old certificate expiry, or sandbox report alone cannot
make this decision. Concurrent renewal either wins those locks or is fenced.

The host sweeps its configured bindings immediately and every five seconds, with
a five-second call budget per binding. It drains saved release intents through the
existing compute reconciler/provider, preserving operation IDs and exact ownership
checks across restarts. This cleanup composition has no acquisition authority.
Provider errors defer cleanup; they do not assert absence. Only verified absence
confirms cleanup. Missing exact provider identity leaves recovery pending.

Replacement work remains pending until a durable recovery decision can authorize
a new Attempt. In particular, PENDING/UNKNOWN command outcomes remain ambiguous;
expiry, cleanup and final reports never acknowledge or replay them. This step
implements the failure-to-fence/release path, not automatic replacement admission
or a safe-retry classifier. The user explicitly selected preserving ambiguous work
for recovery. Durable Session continuity and Execution identity are preserved.

## Stop and report ordering

Agentd cancels pending command contexts before disconnect cleanup, without waiting
for a pending launch before beginning cleanup. The process operation gate and
cancelled-launch cleanup prevent overlapping work. Reconnect still requires a
fresh durable connection epoch; credentials and the bootstrap proof are never
replayed as a recovery shortcut. Configuration equality uses serialized contract
values rather than protobuf implementation caches.

On local cancellation of an established stream, transport gets at most five
seconds for managed-process stop and one final authenticated observation; the
original authority/certificate deadline can shorten that window. An unestablished
connection cancels immediately. No commands execute during the final-report loop.
Managed-process stop gets up to four seconds, reporting/receipt up to one second.
Supervisor cancellation starts the one permanent Shutdown operation during that
window; a timed-out reporting wait does not restart its cleanup budget. The binary
skips a second disconnect-stop pass and joins the same shutdown before exit. The
existing Shutdown budget plus five seconds therefore remains the Pod-grace gate.
Busy or unresolved stop reports `unresolved`. The sender waits only for a matching
transport acknowledgement, never for a lifecycle transition. Broken/expired
transport cannot report and never delays cleanup indefinitely.

The existing `process-control.v1` capability admits a closed PROCESS_STATUS
observation with schema `process-control.v1/shutdown` and exactly one of:
`{"managed_process":"stopped"}` or `{"managed_process":"unresolved"}`.
It has no operation, request digest, harness handle or artifact reference.
AR validates current authority, binding, epoch and sequence as for other frames,
then logs a bounded hint and acknowledges receipt. It never treats the hint as
Execution completion, an operation outcome, checkpoint proof or sandbox absence.
Permanent supervisor shutdown, process-group cleanup and PID-1 reaping still run
before exit; their safe final local log reports success or failure independently
of transport availability (ADR-0036).

## Compatibility and limits

No protobuf, database migration, cross-component contract or dependency changes.
The observation schema is additive within the existing optional process capability;
older AR policy rejects it safely, without preventing local termination. The
materialization's termination-grace gate remains in force.

Trusted homelab evidence publication and integrated live acceptance remain open.
This decision does not claim live Kubernetes destruction/recreation or complete
Phase 4. See [failure-handling evidence](../evidence/agd-020-failure-handling.md).
