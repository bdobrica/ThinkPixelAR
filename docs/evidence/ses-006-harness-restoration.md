# SES-006: Fresh supervised Codex restoration

Verified 2026-09-27 on Linux amd64 with the digest-pinned Codex 0.155.0 binary.

`NewProcessesWithCodexRestore` connects trusted rollout selection to agentd's
existing supervised launch. It clones bounded checkpoint bytes, verifies their
digest and session metadata, restores only the derived rollout path into a new
private home, initializes Codex and resumes the exact thread. Startup does not
issue a turn. Existing command replay, process-group cleanup and admission
boundaries remain in place. Failed resume cannot fall back to a new conversation;
another Start/Restart cannot silently rewind to the same checkpoint.

Passed:

```sh
THINKPIXELAR_TEST_CODEX_BINARY=/home/bogdan/.codex/packages/standalone/releases/0.155.0-x86_64-unknown-linux-musl/bin/codex \
GOCACHE=/tmp/thinkpixelar-go-cache \
/home/bogdan/.local/go/bin/go test -race \
  ./internal/app/agentd ./internal/adapters/harness/codex -count=1

GOCACHE=/tmp/thinkpixelar-go-cache /home/bogdan/.local/go/bin/go vet \
  ./internal/app/agentd ./internal/adapters/harness/codex
```

`TestPinnedCodexRestoredSupervisor` runs two real supervised processes with a
deterministic loopback Responses server. It completes the first turn, saves the
single rollout, stops/reaps the original process and verifies home removal.
It restores into a new home/process, verifies the same thread and no model call
during resume, then verifies that a second explicit turn contains the prior
conversation. Old home credential/config paths and inherited environment canaries
are absent; repeat import is rejected. Temporary workspace paths and loopback
model configuration are test-only overrides. No provider credentials are used.

Focused tests cover digest/version/thread/workspace mismatch, malformed or
truncated JSONL, duplicate metadata, size bounds, immutable selection, mandatory
thread support, command replay, mismatched resume identity, failed-child reaping
and home cleanup. Existing supervisor and Codex package tests also passed under
the race detector.

This is local process restoration evidence. It does not qualify live Kubernetes
replacement, durable export/publication, authenticated checkpoint delivery,
Session worker/readiness composition, fresh AG grants or external credential
revocation. Those integration requirements remain tracked in SES-005, CDX-012
and SES-007. Rollout text remains untrusted/confidential; structural exclusion of
auth/config files is not proof that arbitrary conversation content lacks secrets.
