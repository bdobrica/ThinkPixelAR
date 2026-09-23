# HNS-003 harness conformance framework

Date: 2026-09-23.

## Scope

`test/conformance/harness` exports a reusable factory-driven `Run` suite through
the HNS-001 port. Five independent cases cover descriptor/selection, negotiation,
first-turn lifecycle/events/cancellation, replay/conflict/stale-generation checks
and refusal of unadvertised optional operations. Setup and cleanup belong to the
adapter-specific fixture. No Codex types, production adapter, dependency or
external contract changes are introduced.

The deterministic memory adapter exercises the suite itself. Sixteen deliberate
defects verify rejection: descriptor drift, permissive negotiation, bad handle,
missing readiness, event gap/size/classification, ignored cancellation,
interrupt/close failure, changed replay handle/stream, accepted start/execute
conflicts, stale status acceptance and unsupported resume acceptance.

See the [suite guide](../../test/conformance/harness/README.md) for adoption,
watchdog/cleanup requirements and the exact qualification boundary.

## Verification and limits

- `go test -race ./test/conformance/harness`: passed, including all five cases
  and sixteen broken-adapter self-tests.
- `make verify`: passed (exit 0), including protocol/OpenAPI drift, hygiene,
  formatting, vet/staticcheck, unit/race suites, vulnerability/dependency/license
  checks and binary builds. Opt-in database/Docker/live-provider tests were not
  enabled by this run.
- Repository hygiene and staged whitespace checks passed. Unrelated working-tree
  edits are excluded from the commit.

The suite is independent of paid infrastructure. Its self-test does not run a
real process, Codex, PostgreSQL, mTLS or Kata. Existing AGD evidence retains its
own scope. HNS-004 event mapping and the CDX packaged adapter will supply the
next executable integration; complete fault/optional-feature qualification is
not inferred from this baseline. No new architecture decision is introduced.
