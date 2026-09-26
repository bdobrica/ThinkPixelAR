# CDX-005 thread resume

Date: 2026-09-26. Local Linux amd64, pinned Codex **0.155.0**; executable
fingerprint checked before launch.

`Client.ResumeThread` resumes an exact vendor UUID from state already present in
an isolated child home. It explicitly sets the trusted workspace, approval
`never`, and read-only sandbox policy, and requests `excludeTurns` to avoid
hydrating historical content into the reply. It validates the returned identity,
version, workspace, non-ephemeral idle state and no-network policy. No vendor path,
inline history, fallback creation, automatic retry or authority is introduced.

Unlike creation, the pinned resume method returns without `thread/started`.
Its correlated idle notification is accepted before the response. Historical
usage/goal hints preceding the next turn acceptance are bounded and discarded;
they do not become observations or accounting for the new Execution.

## Verification

```sh
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex \
  go test -race ./internal/adapters/harness/codex ./internal/app/agentd -count=1
```

The focused race suite passed with the real executable enabled. The new
`TestPinnedThreadResume` completes a turn against a loopback Responses fixture,
terminates and reaps the process, initializes a fresh process using the retained
temporary home, resumes the same thread, and completes another turn. The second
model request contains the first assistant response, proving conversation
continuity. A fresh empty home rejects the same thread ID. No provider credentials
or external model calls are used.

Protocol tests cover exact request fields, changed identity/workspace, expanded
permissions, non-idle state, malformed replies, missing results, bounded hint
floods, local replay and cancellation without retry/fallback. Historical hints
from another thread or outside resume are rejected. Focused `go vet` passed.

## Limits

This qualifies the driver and retained local vendor state, not trusted checkpoint
publication/restoration, sandbox reconstruction, or the public Session resume API.
Those remain CDX-012 and Phase 6. Agentd still creates disposable homes and rejects
thread-enabled RESTART; no resume capability is advertised on its wire protocol.
Runtime/checkpoint compatibility, integrity and fresh authority must be checked
by trusted composition before calling the driver. No new OCI build, ARM64/Kata
resume run, live LLMGW call, or full HarnessAdapter conformance is claimed.
