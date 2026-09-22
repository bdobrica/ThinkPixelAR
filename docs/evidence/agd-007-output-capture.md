# AGD-007 — bounded output capture

Implemented 2026-09-22 under [ADR-0031](../adr/0031-agentd-bounded-output-capture.md).

Capture/process tests passed three consecutive race-detector runs:

```sh
go test -race ./internal/app/agentd -run 'TestCapture|TestCaptured|TestProcess' -count=3
go test -race ./internal/app/agentd
make verify
```

The final focused run also includes the constructor/configuration-copy test.
Real test children write stdout in split chunks, an unterminated final record,
and stderr. The consumer receives only redacted records with correct process ID,
source and monotonic local sequence. Restart creates a separate capture and stale
process lookup is rejected. An output-flooding child is stopped and reaped when
the consumer stalls. Stop with a full queue is bounded by the exit-drain budget,
not the longer liveness/backpressure window.

Queue tests cover byte and event-count exhaustion, producer wake-up on consumption,
consumer cancellation, explicit Close, oversized lines/events, sanitizer expansion,
failure/panic sanitization, formatting protection and exact canary redaction across
Write boundaries. Unknown content is wholly suppressed by the production default;
the exact-match test sanitizer is a fixture, not a general vendor-content policy.
Terminal failure clears the queue and remains observable instead of reporting EOF.

`make verify` passed: generated drift, hygiene, supported versions, formatting,
static analysis, repository-wide unit/race tests, vulnerability/dependency checks,
binary builds and OpenAPI verification. Staged whitespace, formatting, repository
hygiene and changed Markdown local links also passed. These are local Linux
process checks, not a deployed transport or Codex event-normalization test. No
cluster changes, paid infrastructure, new dependency or persistent payload fixture
was needed. Binary composition remains AGD-020; Phase 5 supplies registered vendor
schemas and sanitizers. Raw protocol/process output is never platform telemetry.
