# ADR-0031: Agentd bounded output capture

Status: Accepted 2026-09-22

## Decision

AGD-007 adds opt-in `NewProcessesWithCapture` to ADR-0030's controller. The
existing constructor retains null standard streams. Captured processes receive
separate stdout/stderr pipe writers and a fresh Capture for each process ID.
Stdin, environment, launch configuration and authority semantics are unchanged.

Stdout/stderr are newline-framed byte records, with a final unterminated record
at EOF. Records are bounded before sanitization; oversized records fail rather
than emitting a potentially secret-bearing prefix. Complete adapter records
enter through EmitEvent. They are candidate content, not canonical Runtime Events;
vendor interpretation and normalized event mapping remain Phase 5.

A shared in-memory queue enforces the configured Limits buffered event count and
aggregate payload bytes, beneath ADR-0002 ceilings. Diagnostic/event record sizes
use their respective Limits. Each accepted record has process ID, source and a
monotonic local sequence. Per-stream ordering is preserved; cross-stream order
is enqueue order, not a claim about the child's write timing. One adapter event
producer and one consumer are supported. Concurrent event producers fail closed.
The two pipe writers and event producer have bounded in-flight records in addition
to the queue and OS/exec pipe buffers. There is no disk spill or unbounded history.

Sanitization happens before queue admission. The default replaces the entire
unclassified record with `[REDACTED]`, including arbitrary credentials or hidden
reasoning. It does not attempt to publish arbitrary text using regex heuristics.
A registered adapter may supply a trusted, synchronous, bounded, thread-safe
OutputSanitizer which owns its field allowlist, hidden-reasoning exclusion,
credential-key/exact-secret/pattern redaction and schema validation. It must not
block on I/O, retain input or log content. Callback errors and panics become one
fixed failure; output expansion is bounded too. Inputs are privately copied and
cleared. Standard formatting of Output/Capture suppresses content; consumers
must still treat returned content as Confidential and authorize its destination.

Queue saturation backpressures pipe reads/child writes. If no capacity becomes
available within the configured liveness window, capture fails, clears queued
content, wakes consumers/producers and kills the child group. Oversize, redaction
and I/O failures take the same path. The stream returns a fixed terminal error
rather than silently dropping records; the controller prohibits another launch
after a failed capture. A caller's Close explicitly abandons the capture and also
stops its live child. Receive cancellation alone does not discard queued data.

After leader exit, pipe draining/EOF-tail publication are bounded by KillWaitMS.
Go exec WaitDelay closes pipes retained by escaped descendants; a separate timer
also releases writers blocked on the queue. Timeout reports stream failure.
This preserves bounded Stop even when no consumer is reading. As in ADR-0030,
escaped processes themselves require external containment/destruction.

## Consequences

This introduces no dependency, wire schema, log sink or automatic persistence.
Real-process and queue race tests cover redaction across write boundaries,
partial records, byte/count/record bounds, backpressure, terminal failure,
restart identity separation and cleanup. Structured status/heartbeat is AGD-008;
authenticated dispatch is AGD-020. This is not a live Codex/Kubernetes qualification.
