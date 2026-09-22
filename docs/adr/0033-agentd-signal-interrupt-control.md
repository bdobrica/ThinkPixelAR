# ADR-0033: Agentd registered signals and cooperative interrupt

Status: Accepted 2026-09-22

## Decision

AGD-009 adds Controls around the existing Processes controller. Trusted
composition supplies a copied registry of at most 32 negotiated signal handlers
with bounded lowercase names and a command-byte limit no wider than bootstrap or
protocol ceilings. There is no arbitrary POSIX signal-number API. Cancel and
interrupt are reserved rather than custom signal names. Each handler validates
its registered normalized payload schema; unknown names, oversized payloads,
missing handlers and stale/exited process IDs fail before delivery.

Signal and Interrupt share the process-operation gate with Start/Stop/Restart.
Handlers receive the exact current local process ID and a finite context. Signal
payloads are private copies, cleared after the handler returns. Handlers are
trusted adapter code: they must honor cancellation, avoid payload logs, and do no
deferred process work after returning. Errors and panics are reduced to fixed
control failures. Completed Signal rejection does not stop the process.

Interrupt accepts a closed cancel/deadline/shutdown reason and invokes the
optional cooperative protocol handler. A nil result acknowledges protocol
handling only; it does not establish stopped work or terminal Execution state.
The dispatcher/adapter must subsequently observe operation status and request
bounded Stop if work does not quiesce. Unsupported, failed, panicking or
unacknowledged interruption immediately uses the existing bounded TERM/KILL path.
ErrInterruptEscalated reports that fallback Stop completed; Stop failures and
caller deadline expiry retain their fixed errors.

Callback time is capped by StopGraceMS, narrowed by the caller deadline. The
overall call budget adds the existing stop budget. On callback timeout, cleanup
stops the target using an independent finite budget. The gate remains held until
the callback actually returns, even if it ignores cancellation, so late delivery
cannot target a replacement process. There is one pending callback, no retry or
queue. A successful acknowledgement lost to caller cancellation also triggers
target cleanup. Status/heartbeat reads remain available while control is pending.

## Consequences

Real-process tests exercise registered notification and cooperative interrupt,
TERM-resistant fallback, stale IDs, copied registry/payload, input bounds,
sanitized failures and cancellation with a late callback. Non-Linux construction
fails closed. No dependency or wire/schema changes are introduced.

This implements the local bridge, not vendor-specific signal schemas or Codex
turn cancellation (Phase 5). Authenticated dispatch, negotiated handler selection,
command replay/fencing and post-ack operation reconciliation remain AGD-020.
Supervisor SIGTERM/shutdown integration remains AGD-012. No observation or
callback acknowledgement becomes AR/AG lifecycle authority.
