# HNS-004 normalized candidate events

Date: 2026-09-23.

The harness port now defines 21 registered candidate types, typed payload families
and their capability/projection rules. A declaration check requires the v1 schema,
Confidential classification and negotiated capabilities. Conformance uses registered
progress candidates and detects unregistered authoritative names and unnegotiated
tool events. See [ADR-0046](../adr/0046-harness-candidate-events.md) and the
[mapping contract](../contracts/harness-events.md).

Registry tests cover every mapping, including the lack of direct lifecycle,
checkpoint and accounting projections. Negative tests cover authoritative/unknown
names, schema mismatch, classification downgrade and absent capabilities. The
conformance self-test now includes eighteen deliberately broken adapter variants.

Verification on 2026-09-23:

- `go test -race ./internal/ports/harness ./test/conformance/harness`: passed.
- `make verify`: passed (exit 0), including protocol/OpenAPI drift, hygiene,
  formatting, vet/staticcheck, unit/race tests, vulnerability/dependency/license
  checks and binary builds. Opt-in database/Docker/live-provider tests were not
  enabled by this run.
- Repository hygiene and staged whitespace checks passed. Unrelated working-tree
  edits are excluded from the commit.

This is a definition and declaration-check step, not a complete ingestion pipeline.
Typed payloads alone do not enforce closed decoding, redaction, current authority,
replay or durable publication. Actual vendor mapping and hidden-reasoning/credential
canaries follow with the pinned Codex protocol. No new dependency, public schema,
database migration, Codex execution or live Kata qualification is claimed.
