# CDX-007 — pinned streamed candidate events

Implemented 2026-09-26 under
[ADR-0046](../adr/0046-harness-candidate-events.md) and the
[candidate contract](../contracts/harness-events.md). No new public event enum,
authority rule, database migration or dependency is introduced.

`internal/adapters/harness/codex/events.go` opens one pull subscription after an
accepted turn. `event_mapping.go` translates pinned notifications into registered
Confidential candidates with AR UUIDv7 identities, immutable supplied correlation
and consecutive per-operation sequence. It never publishes directly into the
durable Runtime Event registry.

The mapping uses the pinned 0.155.0 schema export and actual App Server output.
The [official App Server documentation](https://learn.chatgpt.com/docs/app-server)
describes item lifecycle notifications and message/process deltas; the exact
pinned protocol controls compatibility. Real output also includes `emittedAtMs`
and account rate-limit notifications, which are validated/discarded respectively.

## Exercised behavior

- Real pinned binary: isolated initialize/thread/turn, loopback Responses SSE,
  `execution.started`, `message.delta`, `message.completed`, then stream EOF.
  One fixture request, no provider credentials or inherited operator state.
- Fixture protocol: command/process start, protected output reference and exit;
  MCP failure and file-change completion; stable item identities and message
  sequence ranges. Repeated lifecycle payloads do not allocate duplicate events.
- Reasoning and credential canaries: reasoning never reaches policy or candidates;
  tool arguments/results, commands, working directories and raw output never
  become inline events. Nil policy suppresses message/process content. The test
  policy is a fixture, not a general secret detector.
- Closed failures: wrong thread/turn, missing starts/deltas/completions, conflicting
  lifecycle repeats, unknown methods/fields, server approval requests, duplicate
  JSON keys, invalid UTF-8/controls, excessive depth/size, truncation, capability
  mismatch and sequence exhaustion. Cancellation/Close unblock pending reads;
  stream failures remain sticky.

Verification passed: the affected Codex, harness-port and agentd packages under
`go test -race` (including the real pinned executable); focused `go vet`; both
`thinkpixel-agentd` and `thinkpixelar` binary builds; changed documentation links
and scoped diff whitespace checks. No PostgreSQL behavior changed or database
test was needed for this mapping.

## Reproduce

Use the exact executable fingerprint recorded in
[the compatibility runbook](../operations/codex-compatibility.md). Tests need
permission to launch the child and bind a loopback socket.

```sh
GOCACHE=/tmp/thinkpixelar-go-cache \
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex \
  go test -race ./internal/adapters/harness/codex \
  ./internal/ports/harness ./internal/app/agentd -count=1
```

To consume the implemented seam, call `Client.Events(handle, operation,
capabilities, limits, policy)` after `StartTurn`. Supply the trusted current
HarnessBinding, the original Execute operation/digest, negotiated capabilities
and bounds. Require `structured-events` and `streaming`, plus tool/process
capabilities for those item types. Iterate `Next(ctx)` synchronously and recheck
authority/fencing at trusted ingestion. A nil policy is safe suppression; passing
content requires a trusted policy that handles fragment boundaries. On a stream
error, stop/reconcile the supervised child; `Close` alone is not an interrupt.

## Limits

The supervisor's existing authenticated EXECUTE path still acknowledges turn
acceptance. This change supplies the adapter stream, not its authenticated
candidate transport/ingestion composition, durable Session publisher or SSE API.
No current-authority assertion is inferred from caller-supplied stream metadata.
The subscription is single-use and has no reconnect/replay recovery. Unsupported
item types fail closed, including experimental dynamic/subagent/image/review
items. Only explicitly mapped message, command, MCP, file-change and web-search
items are supported; selected payload fields are validated, not the entire vendor
application schema. EOF after `turn/completed` is only a stream boundary.

Completion/usage capture (CDX-008), cooperative interruption (CDX-009), public
event delivery, live governed model/tool calls, OCI rebuild and Kata qualification
were not performed or claimed here. The existing runtime image needs a later
rebuild to include this code.
