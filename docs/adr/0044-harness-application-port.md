# ADR-0044: Minimal harness application port for the Codex demo

Status: Accepted 2026-09-23

## Decision

Implement the existing HarnessAdapter contract in `internal/ports/harness`.
The interface covers descriptor/negotiation, start/resume, execute, signal,
interrupt, checkpoint preparation, status and close. Requests carry the existing
operation identity/digest, current Attempt fence and deadline. Handles retain
AR process/binding identity separately from an opaque vendor-session reference.
Credential inputs are references only; the port neither issues authority nor
publishes vendor state. Existing binding states and classification types are reused.

Use the twelve registered capability names and REQUIRED/SUPPORTED/UNSUPPORTED
levels from the normative contract. Missing capabilities are unsupported;
unknown declarations and levels are invalid, and unknown requirements fail closed.
Capability support is not permission, including native fork (ADR-0003).

Use a pull-based event stream: one reader calls cancellable Next; implementations
must bound buffering by negotiated limits. EOF and stream Close do not imply
Execution completion or harness termination. Events are candidate envelopes,
separate from persisted Runtime Events. HNS-004 supplies registered mapping rules;
this port introduces no vendor event names or canonical lifecycle transitions.

## Scope and consequences

This is a Go application boundary, not a new external wire contract. Version ranges
are declarative minimum/maximum bounds; HNS-002 owns bounded selection/parsing.
There is no default adapter, plugin loader, negotiation engine, transport driver,
generic retry executor or automatic unsupported-operation fallback in HNS-001.
Implementations must enforce the documented validation and idempotency obligations;
these types alone do not enforce runtime authority, payload limits or persistence.

Proceed directly through the small registry, conformance and event steps to Codex
startup/thread/turn/events/interrupt and a Kata-hosted turn. Resume/checkpoint/fork
may remain unsupported in the first turn demo. No Codex or live Kata execution is
claimed by this port. The existing full contract remains the target; optional
capabilities are advertised only when implemented and qualified.
