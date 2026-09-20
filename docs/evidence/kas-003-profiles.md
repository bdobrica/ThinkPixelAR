# KAS-003 — Runtime Profile loading

Date: 2026-09-20. Implements [ADR-0007](../adr/0007-runtime-profile-loading.md).

Uses the schema source directly through Go embedding. Dependency justification:
`github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` supplies Draft 2020-12 rather than
an incomplete handwritten validator; `github.com/cyberphone/json-canonicalization
v0.0.0-20241213102144-19d51d7fe467` supplies RFC 8785 ordering/number/string rules
and duplicate-key rejection. Both licenses were reviewed in the module source
and added to the dependency inventory. No runtime schema network fetch is needed.

Focused race tests passed for every schema-required field and unknown fields at
each object level; cross-field resources/GPU/security/network checks; duplicate
keys/names and malformed/oversized JSON; canonical digests; copy isolation;
concurrent lookups; atomic failed reload; mandatory resolver and sanitized errors.
Command: `go test -race ./internal/config/runtimeprofiles ./internal/domain/runtimeprofile`.

Implementation-reference capability checks are an explicit trusted resolver seam;
provider mapping and physical enforcement are later KAS items. This loader does
not grant Execution authority or qualify a named runtime.

`make verify` passed the full source gate, including static analysis, race tests,
vulnerability/license checks, builds and OpenAPI drift validation.

## Contract correction — 2026-09-20

The `none` network class still permits mandatory platform control, including
required trusted DNS. Removed a cross-field check that incorrectly required
DNS to be disabled. A regression test loads the offline profile with trusted
DNS; focused race tests and `make verify` pass. This follows the existing
[network contract](../contracts/network-profiles.md), without widening workload
egress.
