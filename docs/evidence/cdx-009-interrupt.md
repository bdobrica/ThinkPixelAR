# CDX-009: cooperative turn interruption

Completed 2026-09-26 against the exact Codex 0.155.0 amd64 executable and
stable schema fingerprints in [compatibility](../operations/codex-compatibility.md).
The consumed schemas are `v2/TurnInterruptParams.json` (`threadId`, `turnId`)
and `v2/TurnInterruptResponse.json` (empty object). The
[official App Server guide](https://learn.chatgpt.com/docs/app-server) describes
cooperative cancellation; the pinned executable tests establish this scope.

## Implemented and verified

- The driver sends a single interrupt for its accepted turn, validates the
  response, and caches success/failure for replay. Cancellation bounds a blocked
  write/read and closes the protocol; it never signals the process itself.
- The streaming reader handles interleaved acknowledgement. Without an active
  reader, a bounded handoff retains notifications for subsequent normalization.
  Both terminal-before-ack and ack-before-terminal orders are tested, along with
  malformed/rejected/missing responses, retention overflow and timeout.
- Real pinned Codex opens a loopback Responses SSE request, accepts interrupt,
  and emits the separate `interrupted` completion candidate. No provider keys or
  live model service are used, and raw protocol data is not logged.
- The authenticated agentd turn exchange now uses cooperative interrupt before
  bounded stop/reap. Supervisor fixtures cover acknowledgement, rejection,
  timeout and command replay. Existing authority, operation and handle checks
  remain in force; no lifecycle or AG accounting authority is inferred.

Reproduction (the loopback integration fixtures need permission to bind sockets):

```sh
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex \
  go test -race ./internal/adapters/harness/codex \
  ./internal/app/agentd ./internal/ports/harness -count=1
```

The above focused race suite passed, including the real pinned tests. `go vet`
passed for these packages, and both `thinkpixel-agentd` and `thinkpixelar` built.
The initial sandbox run could not bind loopback sockets; verification was rerun
with that permission. Test fixtures now supply the same command-size limit
required by the production controls constructor.

## Limits

The driver supports one accepted turn per process. Interrupt acknowledgement is
not turn completion; process-control INTERRUPT still means bounded process stop,
including after acknowledgement. It does not wait for terminal event publication.
STOP, disconnect and shutdown continue to enforce external termination directly.
Authenticated candidate delivery/durable publication remain composition work;
full HarnessAdapter conformance, a rebuilt OCI image and real Kata execution are
not claimed by this change. See [stream contract](../contracts/harness-events.md)
and [process contract](../contracts/agentd-protocol.md).
