# ADR-0032: Agentd process observations and heartbeat

Status: Accepted 2026-09-22

## Decision

AGD-008 adds immutable atomic process-status snapshots alongside ADR-0030's
serialized lifecycle operations. Status reads never acquire the operation gate
or wait for launch, termination or output draining. The zero state is ABSENT;
launch, running, stopping/draining, exit and failure use the existing v1 Heartbeat
state vocabulary. A terminal observation cannot be overwritten by a racing
stop/drain notification for the same process ID. Restart gets a fresh identity.

ProcessStatus exposes only local process ID, state, whether an exit was observed,
numeric exit code/signal and a closed failure reason (launch, unexpected exit,
cleanup or output). Expected signal termination is EXITED; nonzero exit or an
unexpected signal is FAILED. Capture failure takes precedence over the resulting
kill signal. These observations do not establish adapter readiness, Execution
success, current authority or provider health. They contain no PID, paths, argv,
environment, output or OS error text.

Processes.Heartbeat creates a fresh v1 Heartbeat with current process state.
RunHeartbeats takes a bounded trusted snapshot callback and mandatory sender for
one admitted connection. The dispatcher adds its actual last accepted/produced
sequences and active operation ID; local capture sequence is not a wire sequence.
Counter regressions, invalid operation IDs, unknown fields and invalid states
fail before sending. There is no new wire schema or status payload registration.

An immediate heartbeat is followed by the negotiated periodic interval, with
coalesced ticks and at most one send in flight. The interval and liveness window
must satisfy the existing protocol bounds. Each send has a deadline of one
heartbeat interval, narrowed by connection context. Failure/timeout terminates
the loop with a fixed error; there is no retry or unbounded send queue. Cancellation
stops the ticker and cancels the sender. The trusted sender must honor context;
even a misbehaving sender causes no further sends after timeout, though its one
outstanding callback cannot be forcibly killed by Go.

The sender belongs to the shared authenticated outbound dispatcher: it allocates
envelope sequence/IDs, applies frame checks and closes the stream on failure.
Heartbeats do not use the content queue and cannot renew authority, extend
certificate life or reset AR's receive watchdog merely through a local write.

## Consequences

Real-process/race tests cover observations while Stop is active, restart,
successful/nonzero/signalled exits, output failure and terminal-state races.
Heartbeat tests cover cadence, private snapshots, progress validation,
cancellation and stalled/error senders. The binary remains dormant pending
AGD-020 dispatch composition. AGD-009 next adds protocol signal/interrupt control.
