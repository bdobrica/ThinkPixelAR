# AGD-009 — signal and interrupt control

Implemented 2026-09-22 under [ADR-0033](../adr/0033-agentd-signal-interrupt-control.md).

Validation:

```sh
go test -race ./internal/app/agentd -run 'TestControls|TestInterrupt|TestTimedOutSignal' -count=3
go test -race ./internal/app/agentd
make verify
```

The initial control tests passed three race-detector runs. The final agentd suite
adds sanitized Signal error/panic coverage and acknowledgement-cancellation
cleanup via the existing process-operation mechanism. The final agentd race suite
and `make verify` passed: generated-artifact checks, hygiene, supported versions,
formatting, static analysis, repository-wide unit/race tests, vulnerability and
dependency checks, binary builds and OpenAPI verification. Staged whitespace,
repository hygiene and changed Markdown local-link checks also passed.

A real test child handles notification and interrupt signals from registered
test adapter callbacks. Cooperative acknowledgement leaves the process running;
the test does not mistake that acknowledgement for operation completion.
Unsupported/error/panic/timeout interrupt cases stop a child ignoring TERM by
escalating to KILL. A timed-out Signal callback which temporarily ignores context
keeps replacement blocked while cleanup reaps its original target.

Tests also verify copied registrations and payloads, exact process identity after
restart, unknown/reserved names, invalid reason classes, pre-cancelled requests,
nil handlers and command-size bounds including bootstrap-limit narrowing. Invalid
requests never reach handlers; callback error/panic details never escape.

These are local Linux process/adapter-boundary checks. Test handlers use OS signals
to exercise the child; no arbitrary OS signal API is exposed to callers. No live
Kubernetes/Codex protocol or authenticated binary deployment is claimed. No paid
infrastructure, cluster change or dependency was required.
