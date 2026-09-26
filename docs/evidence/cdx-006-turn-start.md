# CDX-006 Execution input to Codex turn start

Date: 2026-09-26. Pin: Codex **0.155.0**, Linux amd64 fingerprint verified before
real execution. Contract: [codex-turn.v1](../contracts/agentd-protocol.md#optional-codex-turn-start).
Composition: [Codex runbook](../operations/codex-compatibility.md#enable-the-first-turn).

The application mapping accepts a fenced `harness.ExecuteRequest` with one bounded
inline text input. The trusted plan sends EXECUTE only after START. Materialization
pins its operation/digest; PostgreSQL checks live authority, current Attempt/epoch,
and acknowledged HarnessBinding startup before committing the dispatch claim.
Agentd sends the input as one Codex text item on its existing thread and acknowledges
only the matching turn-start response. Vendor turn identity remains private to the
driver. No canonical event, Execution success or accounting fact is inferred.

## Reproduce

With the existing pinned executable and a migrated disposable PostgreSQL database:

```sh
export THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex
export THINKPIXELAR_TEST_DATABASE_URL='<disposable PostgreSQL test database URL>'
go test -race ./internal/adapters/harness/codex \
  ./internal/adapters/sandboxtransport/control \
  ./internal/app/agentd ./internal/app/agentdserver \
  ./internal/adapters/postgres -count=1
```

All five packages passed with both variables set, using the existing isolated
`thinkpixelar_cdx004` test database. Local TCP/Unix socket and database access were
required. Focused `go vet` and builds of both shipped binaries also passed.

Verified:

- Real pinned App Server initialize, thread creation and turn acceptance, with
  replay retaining the same turn ID. A test-only loopback model endpoint returns
  an error; no provider credential is supplied, and no model completion is claimed.
- Authenticated transport sends START, EXECUTE, identical EXECUTE replay and
  INTERRUPT through agentd to a protocol child fixture. Input reaches exactly one
  turn; process-control INTERRUPT still terminates/reaps the process.
- Application mapping preserves input/operation identity and copies content;
  mismatched fence, unsupported options/artifacts/schema/classification, invalid
  UTF-8, oversized content, expired deadlines and changed digests fail closed.
- Protocol tests reject wrong/duplicate RPC IDs, vendor errors, invalid turn IDs,
  terminal responses, server requests and oversized/truncated frames. Cancellation
  leaves an unknown outcome and cannot resend the turn-start request.
- PostgreSQL rejects EXECUTE before startup acknowledgement, modified text with a
  recomputed digest, wrong operation/handle/epoch/Attempt and duplicate dispatch.
  A new policy instance sees PENDING then ACKNOWLEDGED after the matching report.
  Snapshot, command journal and sequence rows contain no prompt canary.

## Limits

This implements the first turn-start operation through existing application and
transport seams. It does not implement the public Execution submission service,
a complete HarnessAdapter, event streaming, completion/usage mapping, cooperative
Codex interrupt, resume or vendor-file checkpoints. One Execution operation is
allowed per process; further turns require the later lifecycle work. Protocol
output remains private under bounded pipe backpressure after acceptance.

Provider/LLMGW configuration, a completed live-model turn, Kata qualification and
runtime-image rebuild were not performed. The test-only model route is not
production configuration. Current phase sequencing remains in PLAN/TODO.

The [official App Server documentation](https://learn.chatgpt.com/docs/app-server)
was consulted for turn-start semantics. The exported stable schema and actual
pinned binary determine the supported request/response shape used here.
