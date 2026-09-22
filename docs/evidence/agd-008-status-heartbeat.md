# AGD-008 — process status and heartbeat

Implemented 2026-09-22 under [ADR-0032](../adr/0032-agentd-status-heartbeat.md).

Validation commands:

```sh
go test -race ./internal/app/agentd
go test -race ./internal/app/agentd -run 'TestHeartbeat|TestProcess.*Status' -count=3
make verify
```

The agentd race suite and three repeated status/heartbeat runs passed.
`make verify` passed generated-artifact checks, hygiene, supported versions,
formatting, static analysis, repository-wide unit/race tests, vulnerability and
dependency checks, binary builds and OpenAPI verification. Staged whitespace,
repository hygiene and changed Markdown local-link checks also passed.

Real Linux children verify ABSENT → RUNNING → STOPPING → EXITED while status and
heartbeat remain available during the blocked Stop operation; restart reports a
fresh process ID. Launch failure, nonzero exit, unexpected signal and output
backpressure failure have closed reason codes. Successful natural exit preserves
code zero. Returned snapshots are private; late stop notification cannot regress
terminal status.

Heartbeat tests verify immediate/periodic sends, connection progress and operation
identity, private protobuf copies, cancellation, fixed send errors and bounded
timeout even with a sender temporarily ignoring context. Invalid state, unknown
fields, invalid UUID, counter regression and incompatible timing limits fail
before delivery. No process content is included in status/heartbeat.

These are local process and sender-boundary checks. The sender in these tests is
an explicit fixture; authenticated envelope dispatch and sequence ownership remain
AGD-020. No running binary, live Kubernetes heartbeat, Codex readiness or
authority/Execution-success claim is made. No dependency or infrastructure change
was required.
