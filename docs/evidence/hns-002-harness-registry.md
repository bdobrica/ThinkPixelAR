# HNS-002 harness registry

Date: 2026-09-23.

## Scope

`internal/app/harnessregistry` constructs an immutable explicit adapter registry
and resolves exact kind/build/protocol pins against bounded descriptor ranges.
It checks contract version, state format, required capabilities and prerelease
allowance without calling negotiation or lifecycle methods. Descriptor slices/maps
are copied; missing builds never fall back to another registered implementation.
See [ADR-0045](../adr/0045-pinned-harness-registry.md).

## Verification

Focused race tests cover inclusive range endpoints, build selection with multiple
installed builds, wrong kind/build/contract/protocol/state/capabilities, prerelease
opt-in, invalid versions and descriptors, nil adapters, duplicate registration,
cancelled construction, snapshot isolation and concurrent lookups. Identifier and
state-format acceptance matches the published AgentRuntimeSpec spelling.

`go test -race ./internal/app/harnessregistry ./internal/ports/harness
./internal/adapters/sandbox/agentsandbox` passed. The first aggregate run caught
the Kubernetes utility import outside adapters; replacing it with the independent
Go SemVer package fixed that boundary violation without weakening the check.
The dependency gate then identified the missing license inventory entry, which
was added with the upstream BSD-3-Clause license. Final `make verify` passed
(exit 0): protocol/OpenAPI drift, hygiene, formatting, vet/staticcheck, unit/race
tests, vulnerability scanning, dependency/license checks and binary builds.
Opt-in database/Docker/live-provider tests were not enabled. Staged whitespace
and repository hygiene checks passed.

## Limits

Fixtures use synthetic Codex-named descriptors, not qualified Codex versions.
No runtime wiring, live Codex turn, database or Kata qualification is claimed.
The selected adapter must still perform full compatibility negotiation, including
schema/checkpoint/limit/policy validation and actual-handshake matching. The registry
does not interpret arbitrary AgentRuntimeSpec compatibility expressions, acquire
a sandbox, persist negotiation, or grant authority. The bounded SemVer parser uses
the newly direct `golang.org/x/mod` v0.38.0 dependency, justified in ADR-0045.
No schema/migration or cross-component ownership change. Unrelated working-tree
edits are excluded.
