# ADR-0045: Select explicitly registered harness builds

Status: Accepted 2026-09-23

## Decision

HNS-002 adds an immutable application registry of at most 32 explicitly supplied
adapters. Trusted composition supplies each build digest. Construction snapshots
the descriptor's slices/maps, validates registered capabilities and positive
limits, and rejects duplicate kind/build pairs. Different builds of a kind may
coexist; a lookup always pins the exact build. Registry entries are an operator
allowlist, not dynamic plugin discovery or a marketplace authority source.

Resolve checks stable kind, build digest, exact adapter-contract version, exact
packaged harness and agentd versions, state format and required capabilities.
Versions must lie within declared inclusive minimum/maximum ranges, each within
one major version. At most 16 ranges per protocol and 32 state formats are allowed.
Version inputs are canonical semantic versions bounded to 128 bytes.
`golang.org/x/mod/semver` supplies parsing/comparison without provider imports.
Prerelease selection, including the implementation version, requires explicit
allowance. Missing/mismatched inputs fail closed without selecting another build.
No latest-version rule, range-expression evaluator or fallback adapter is added.

Registry selection is deliberately narrower than full compatibility negotiation.
Callers still invoke the selected adapter's Negotiate before acquisition to check
runtime/checkpoint evidence, event schemas, narrowed limits and policy, and verify
the actual authenticated handshake before work. The registry never launches a
process or turns descriptor observations into execution authority. Adapter code
and configuration must remain immutable after registration; the snapshot is not
a sandbox attestation. Descriptor errors are reduced to a fixed safe error.

## Demo consequence

Pin one Codex build and its packaged protocol versions in demo composition.
Follow with small conformance/event mapping steps and the Codex adapter itself.
No real Codex version is qualified here; test descriptors are synthetic. Fork,
dynamic reload, plugin loading and broad adapter lifecycle orchestration are not
prerequisites for getting a Codex turn running in Kata.

## Dependency justification

Use `golang.org/x/mod` v0.38.0 (BSD-3-Clause, Go Authors,
source `https://go.googlesource.com/mod`), an external runtime library owned here
by `internal/app/harnessregistry`. This is the version already selected in the
module graph; it is now a direct dependency. The standard library has no SemVer
parser. Kubernetes' version utility would violate the existing application/adapter
boundary, and a handwritten parser would add unnecessary compatibility risk.
Only the SemVer package is imported; no provider types, new service or authority
is introduced. Checksums and the repository dependency/license gate cover the pin.
