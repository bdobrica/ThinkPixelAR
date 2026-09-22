# ADR-0039: Bounded authenticated process dispatch

Status: Accepted 2026-09-22

## Decision

Add the optional `process-control.v1` capability using existing v1 Command,
Acknowledgement, Heartbeat and Observation fields. It selects only the immutable
bootstrap process configuration. START, STOP, RESTART, INTERRUPT and STATUS have
empty payloads and no artifact reference. EXECUTE, SIGNAL and checkpoint commands
remain unavailable through this capability. INTERRUPT uses bounded process stop;
cooperative vendor interrupts remain an adapter concern.

Configuration identity is SHA-256 of Go encoding/json's serialization of the typed
agentd Config. The request digest covers capability, command enum decimal value,
configuration digest and HarnessHandle, separated by NUL bytes. Connection/time
metadata is excluded so the same operation can be recognized after reconnect.
Each command requires UUID operation/handle identifiers and an unexpired deadline.

The dispatcher accepts one process handle per supervisor lifetime. Restart keeps
that correlation handle while the process controller creates a fresh local process
ID. The first START reserves the handle even if launch fails. Later START operations
cannot reuse the supervisor to launch another process. The local ledger reserves
an operation before execution and retains up to 128 results, including failures;
conflicting keys and new operations after exhaustion fail closed without eviction.
An identical retry acknowledges the original result; current health is reported
separately and cannot be inferred from that acknowledgement.

Each connection has one sender, one receiver and one command worker. Per-direction
sequence checking accepts only the next sequence or an exact copy of the latest
frame. Worker and stream gates prevent overlapping process-control owners. Process
operations use finite command deadlines while heartbeats and bounded capture share
the sender. Capture limits narrow to negotiated limits before dispatch. Output uses
registered stdout/stderr/event schemas and the existing sanitizer; the constructor
suppresses unclassified content. There is no output log sink or durable event claim.
Disconnect stops the process and abandons capture. Credential renewal requests wait
for the current command; an unsolicited rotation response is rejected.

## Scope

This implements the sandbox dispatcher and its optional wire semantics. It does
not replace AR's mandatory durable command reconciliation, authority/materialization
policy or effective-provider verification. The executable remains dormant until
that trusted composition is wired. No fixture authorizer is installed in a binary.
AGD-020 and AGD-019 therefore remain open. The required composition is unchanged
from ADRs 0027, 0037 and 0038.

See [dispatcher evidence](../evidence/agd-020-process-dispatch.md).
