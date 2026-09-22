# ADR-0034: Agentd bounded checkpoint preparation

Status: Accepted 2026-09-22

## Decision

AGD-010 adds an opt-in Checkpoints component alongside Processes. Trusted
composition registers the bound runtime's sorted, non-overlapping durable roots
beneath `/state`, its negotiated state format, and a checkpoint-capable adapter
hook. Registration is copied and limited to 32 roots, 1024 bytes per path and 128
bytes for the format. There is no command-supplied root or default hook.

The hook flushes/quiesces the exact current process and synchronously invokes a
consumer exactly once while its preparation window is open. It releases
quiescence before returning, including failure and cancellation. The consumer
receives a copied candidate manifest: bounded vendor identity, the registered
state format, and at most 64 sorted, non-overlapping paths relative to `/state`.
Only registered roots and descendants are eligible. Traversal, absolute manifest
paths, unclean paths, control characters and backslashes are rejected.

Preparation uses the existing process-operation gate and Controls delivery
mechanism. StopGraceMS caps the hook and consumer together, narrowed by the caller
deadline; existing finite stop budgets cover timeout cleanup. Timeout stops the
target and holds the gate until the hook returns. No replacement may race a late
hook. Adapter and consumer code must honor context and retain no callbacks or
inputs. Errors and panics are reduced to fixed errors; failed preparation never
returns a successful observation. A successful result lost to caller cancellation
uses the existing process cleanup behavior.

## Boundary and consequences

Registration and manifests are lexical declarations, not filesystem evidence.
This component neither opens nor copies files and cannot attest quiescence of an
untrusted harness. Trusted checkpoint/storage code must independently enforce
mount boundaries, reject symlinks and special files, detect scan races, bound
actual sizes, exclude credentials and validate integrity. Consumers must complete
any work requiring quiescence inside the bounded callback; retaining a manifest
does not extend that window. No snapshot, hash, publication or durable checkpoint
status is produced by agentd.

Authenticated admission, capability selection and wire dispatch remain AGD-020;
credential exclusion checks remain AGD-011 and trusted checkpoint publication is
Phase 6. The binary remains dormant. Non-Linux construction fails closed. No
public schema, dependency or cross-component ownership changes are introduced.

## References

- [Agent runtime spec](../contracts/agent-runtime-spec.md)
- [Checkpoint contract](../contracts/checkpoint.md)
- [Agentd contract](../contracts/agentd.md)
- [AGD-010 evidence](../evidence/agd-010-checkpoint-preparation.md)
