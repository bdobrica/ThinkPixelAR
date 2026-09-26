# CDX-008: completion and usage observations

Completed 2026-09-26 against the unchanged [Codex 0.155.0 pin](../operations/codex-compatibility.md).
Protocol references: exported `TurnCompletedNotification.json`,
`ThreadTokenUsageUpdatedNotification.json`, `ErrorNotification.json`, and the
[official App Server documentation](https://learn.chatgpt.com/docs/app-server).
The exact pinned schema and executable determine the tested wire shape.

`Client.Events` now emits the latest reported thread usage once before a terminal
completion/failure observation, then EOF. Result references come only from trusted
`ResultReferencePolicy`. All remain Confidential candidates under
[ADR-0046](../adr/0046-harness-candidate-events.md); no lifecycle or AG accounting
changes are authorized by these observations.

## Verification

- Focused race tests cover completed/failed/interrupted outcomes, latest snapshot
  coalescing, missing usage, unfinished items on failure/interruption, repeated
  terminal frames, reference validation/policy errors, capability checks,
  malformed/negative/overflow/regressing usage and truncated streams.
- Real pinned App Server with a loopback Responses SSE fixture emits message,
  usage (5 input / 2 output tokens), and completed candidates in order.
- Real pinned App Server with a loopback HTTP 400 fixture emits a failure
  observation without leaking the fixture's diagnostic canary.
- Harness port and agentd race tests, focused `go vet`, and both binary builds pass.

Reproduce using the pinned executable from the compatibility document:

```sh
GOCACHE=/tmp/thinkpixelar-go-cache \
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex \
go test -race ./internal/adapters/harness/codex ./internal/ports/harness ./internal/app/agentd -count=1
```

No live provider, PostgreSQL migration, OCI/Kata turn, authenticated event delivery,
durable publisher or AG settlement was exercised. Artifact persistence/access
policy remains trusted composition; tests use fixture references. Cooperative
interrupt dispatch remains CDX-009; interruption mapping here uses a wire fixture.
