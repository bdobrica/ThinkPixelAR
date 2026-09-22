# ADR-0036: Agentd bounded supervisor shutdown

Status: Accepted 2026-09-22

## Decision

AGD-012 adds permanent, idempotent Processes.Shutdown and binds it to the
supervisor's signal-cancelled context through RunProcesses. The binary's existing
SIGINT/SIGTERM handler now shuts down its controller; it still admits no work
until AGD-020 supplies authenticated dispatch. An admitted controller can use the
same runner with its negotiated InterruptHandler.

Shutdown closes command admission before cancelling active operation contexts.
Start, Restart, signals and checkpoint preparation then reject new work. A launch
already inside the OS observes closure after returning and cleans up its child.
The first shutdown call selects the hook and starts one cleanup worker; repeated
calls share its result. Caller cancellation stops waiting, not cleanup.

Cleanup waits for the operation gate for at most StartTimeoutMS + StopGraceMS +
KillWaitMS. It never overlaps a new adapter callback with a pending command. If
the gate remains held, it kills the atomically retained current process group,
waits for the managed child's reaper within the remaining budget, and reports
unresolved cleanup. Admission remains closed permanently.

With the gate acquired, the controller reports STOPPING and invokes the optional
cooperative hook with InterruptShutdown. The callback and subsequent wait for
process exit share StopGraceMS. Acknowledgement alone is not termination. Failed
or panicking callbacks proceed to the existing TERM/KILL path. A callback still
pending at its deadline also triggers termination and an unresolved-cleanup error;
Go cannot forcibly stop adapter code that ignores context. No checkpoint is
attempted during shutdown.

The existing reaper retains the leader PID through process-group cleanup and
then waits for the leader and bounded output drain. After that, PID 1 additionally
reaps adopted children with nonblocking Wait4 until ECHILD or the deadline. This
never races the managed child's exec.Cmd.Wait. Ordinary non-PID-1 controllers do
not reap unrelated application children. Escaped/live descendants still require
external Sandbox destruction; shutdown cannot authorize credential reuse.

The overall independent budget is StartTimeoutMS + 3 × StopGraceMS +
3 × KillWaitMS, covering pending work, cooperative close, TERM/KILL, draining and
orphan reaping. Composition must choose it below the effective Pod termination
grace with room for final reporting. Existing example values yield 28 seconds.
Shutdown returns a fixed failure on unresolved cleanup or deadline expiry.
RunProcesses emits one safe final process-state log; authenticated final frame
delivery and validation against the materialized Pod budget remain AGD-020.

## Verification and compatibility

Real-process race tests cover SIGTERM-driven supervision, graceful TERM handling,
TERM-resistant KILL escalation, hook acknowledgement/error/panic/timeout, a
cancelled caller, blocked command cancellation, launch races and repeated closure.
An isolated Linux subreaper fixture exercises orphan adoption/reaping without
changing the parent test runner or requiring a privileged container.

There is no new dependency, wire/schema change, authority or automatic launch.
Non-Linux shutdown fails closed. See [evidence](../evidence/agd-012-shutdown.md).
