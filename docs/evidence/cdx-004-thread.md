# CDX-004 thread creation and durable identity

Date: 2026-09-26. Pinned Codex: **0.155.0**, local Linux amd64 executable
fingerprint checked before launch. Wire extension:
[required codex-thread.v1](../contracts/agentd-protocol.md#optional-codex-thread-creation).
Enablement: [Codex compatibility runbook](../operations/codex-compatibility.md).

START now optionally creates one non-ephemeral vendor thread after initialization.
The driver checks bounded response/notification correlation, workspace, pinned
version and read-only/no-network policy. Startup hints are discarded; raw vendor
frames and paths are not published. Failed or ambiguous creation is never retried.
Agentd sends only process/thread IDs with the admitted START correlation before ACK.

AR persists those IDs in the existing immutable HarnessBinding and links the
Attempt under its current fence, live grant and connection epoch transaction.
Adapter/build/negotiation metadata comes from trusted materialization. A fresh
tenant-scoped reader returns the handle for checkpoint/continuation composition.
No migration or canonical lifecycle transition is introduced.

## Reproduce

Use a disposable database with the repository migrations applied and an existing
pinned executable. Local socket and PostgreSQL access are required.

```sh
export THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex
export THINKPIXELAR_TEST_DATABASE_URL='<disposable PostgreSQL test database URL>'
go test -race ./internal/adapters/harness/codex \
  ./internal/adapters/sandboxtransport/control \
  ./internal/app/agentd ./internal/adapters/postgres -count=1
```

All four packages passed with both opt-in variables set. Verification used the
isolated `thinkpixelar_cdx004` database because the existing development database
failed migration; it was not repaired as part of this task. Focused `go vet` and
builds of both shipped binaries also passed.

Covered behavior:

- Real pinned Codex thread creation through agentd supervision, local replay and
  disconnect cleanup; timestamped notifications accepted with bounded parsing.
- Authenticated agentd exchange emits correlated identity before START ACK.
- Real pinned thread identity persisted, process terminated, and the same identity
  loaded through a new database reader. This test uses existing authority/provider
  fixtures; it does not claim a live Kata admission path.
- Database tests reject ACK before identity, wrong epoch/operation/handle/digest,
  malformed identity, conflicting replay, cross-tenant reads and revoked reports.
  Identical replay preserves one binding and its Attempt linkage.
- Synthetic protocol tests reject mismatched IDs, duplicate keys, wrong workspace,
  expanded permissions, missing notifications, server requests and hint floods.
  Cancelled creation cannot issue a second request.

## Limits

Only identity metadata is durable here. Codex homes remain disposable; vendor
files/checkpoint publication, resume, turns and live Kata qualification retain
CDX-012, CDX-005, CDX-006 onward and CDX-016. RESTART and a new START for an already
bound Session fail closed until resume exists. No model/provider call, runtime
image rebuild, checkpoint restoration or platform RC qualification was performed.
