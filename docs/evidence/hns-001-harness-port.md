# HNS-001 harness application port

Date: 2026-09-23.

## Implemented scope

`internal/ports/harness` now expresses the normative HarnessAdapter lifecycle,
descriptor, compatibility inputs/results, limits, fenced mutation requests,
neutral process handles, candidate checkpoint/status and pull-based event stream.
It reuses AR operation identities, binding states and data classifications.
Credentials cross the port only as opaque injection references. Stable errors
contain no vendor output. See [ADR-0044](../adr/0044-harness-application-port.md).

Capability checks recognize the twelve contract names, reject invalid declarations
and unknown requirements, and treat absent entries as unsupported. The first turn
demo can require structured events, streaming and interrupt without advertising
resume, checkpoint preparation or native fork.

## Verification

- `go test -race ./internal/ports/harness`: passed. Tests cover missing/unsupported
  capabilities, both supported levels, unknown requirements/declarations, invalid
  levels, complete requirement sets and absence of implicit resume-to-fork support.
- `make verify`: passed (exit 0), including protocol/OpenAPI drift, hygiene,
  formatting, vet/staticcheck, unit/race suites, vulnerability/dependency/license
  checks and binary builds. Opt-in Docker/PostgreSQL/live-provider tests were not
  enabled by this run.
- Staged whitespace and repository hygiene checks passed.

## Limits and next work

No adapter implementation or runtime authority enforcement is added by an
interface. No Codex protocol, process, transport or live Kata test was run for
HNS-001. Version selection and actual handshake matching follow in HNS-002/CDX;
HNS-003 supplies reusable adapter conformance and HNS-004 defines event mappings.
Descriptor bounds, payload validation, idempotency and stream backpressure must
be enforced by those implementations, not inferred from the Go field types.
No external API, database migration, new dependency or integration ownership
change is introduced. Existing unrelated working-tree edits are excluded.
