# ADR-0007: Validate and atomically publish Runtime Profile sets

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Operator profiles become immutable execution snapshots. Missing security fields,
ambiguous JSON, partial reloads or unstable hashes could silently change the
constraints applied to later materialization.

## Decision

Embed the normative Draft 2020-12 schema directly from its documentation source.
Load bounded JSON documents through RFC 8785 canonicalization, schema validation,
abstract cross-field checks and an explicit trusted implementation resolver.
Reject duplicate keys, unknown/missing fields, duplicate names, invalid UTF-8,
resource inversions, inconsistent GPU requirements and weakened secure classes.

A resolver must validate every opaque implementation reference and all required
provider, runtime, architecture, storage, network, process and lifecycle
capabilities. No resolver means no admission. Its complete JSON resolution
evidence receives a separate canonical digest. Concrete infrastructure types
remain in adapters; implementing the resolver is part of provider mapping.

Publish the whole set under a lock only after every profile succeeds. Failed
reloads preserve the previous set. Lookup returns independent values and bytes,
so reload or caller mutation cannot rewrite a prior resolution. Persist canonical
profile and implementation evidence before acquisition. RunAuthority intersection
is a separate per-Execution admission responsibility, not a configuration grant.

## Alternatives considered

Duplicating the schema in Go would create competing authorities. Plain JSON
marshaling is not RFC 8785 canonicalization. Incremental in-place reload would
expose partial configuration.

## Consequences

Two focused dependencies implement Draft 2020-12 validation and JCS; exact pins
and license review are recorded in the evidence. Reload ordering belongs to the
trusted configuration caller. There is no filesystem watcher or automatic
production hot-reload endpoint in this change.

## Security

Profiles never expand Run authority. Invalid configuration errors omit original
profile/provider payloads. Strong classes retain all normative security checks.
A runtime name is not physical isolation proof; only the trusted resolver can
accept qualified implementation mappings, with later live gates still required.

## Operations

Maximum 128 profiles, 64 KiB per document and 64 KiB per reference-evidence object.
Keep a previous valid set on rejection and require explicit successful startup
loading before provider use. Credentials do not belong in profile evidence.

## Compatibility

The published schema is unchanged, including its optional properties. Unknown
versions fail closed. Existing execution snapshots remain immutable; new sets
only affect future resolution.

## References

- [Runtime Profile contract](../contracts/runtime-profiles.md)
- [Schema](../contracts/runtime-profile.schema.json)
- [KAS-003 evidence](../evidence/kas-003-profiles.md)
