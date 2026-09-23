# CDX-001 Codex/App Server pin

Date: 2026-09-23. Selected release: **0.155.0**.

The opt-in `TestPinnedAppServer` launched the existing standalone Linux amd64 musl
executable with the exact fingerprint recorded in
[compatibility policy](../operations/codex-compatibility.md). Local distribution
metadata reported layout 1, version 0.155.0, target `x86_64-unknown-linux-musl`,
entrypoint `bin/codex`. The source executable and operator installation were not
modified. This fingerprint identifies the tested file; it does not independently
attest upstream distribution provenance.

Observed result: PASS. The real binary reported `codex-cli 0.155.0`; generated
312 valid stable JSON schemas with the pinned aggregate fingerprint; exposed the
required thread/start, thread/resume, turn/start, turn/interrupt, turn/completed and
agent-message delta surfaces. Stdio rejected a pre-initialize request, accepted
initialize with the expected build identity followed by initialized, rejected a
second initialize, returned an empty local thread list and exited cleanly on EOF.

The test uses an isolated temporary home and an explicit credential-free child
environment. It does not dump initialization metadata/raw frames/stderr, start a
thread or perform a model turn. No credential, live history or runtime schema
payload is committed. Generated schemas are temporary, not hand-maintained copies.

## Verification

- Explicit real-binary probe with `THINKPIXELAR_TEST_CODEX_BINARY` and
  `go test -race ./internal/adapters/harness/codex -run '^TestPinnedAppServer$' -count=1 -v`:
  PASS, 1.37 seconds (2.398 seconds including race-test overhead), with both
  executable and schema fingerprints enforced.
- `make verify`: PASS (exit 0), including protocol/OpenAPI drift, hygiene,
  formatting, vet/staticcheck, unit/race tests, vulnerability/dependency/license
  checks and binary builds. This separate gate did not enable the opt-in Codex,
  database, Docker or live-provider checks.
- Repository hygiene and staged whitespace checks passed. Existing unrelated
  working-tree edits are excluded from the commit.

## Limits

This closes the tested-version/support-policy task only. ARM64, immutable OCI
packaging, agentd-controlled production handshake, thread/turn execution, events,
interrupt, durable state and live Kata acceptance remain their CDX tasks. Schema
presence is not proof that those methods execute correctly. Use the existing
homelab for later live qualification; no new infrastructure cost is required.
