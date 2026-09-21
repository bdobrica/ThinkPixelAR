# ADR-0022: Agentd protocol and compatibility handshake

Status: Accepted 2026-09-21

## Context

ADR-0002 selects outbound mTLS gRPC with Protobuf and an explicit compatibility
handshake. The sandbox is untrusted. A compatible self-reported build, binding or
capability must never become authentication or execution authority.

## Decision

`thinkpixel.agentd.v1` is the independent wire package, with bidirectional
`AgentTransport.Connect` and typed envelope bodies. The initial minor is zero.
Commands identify preconfigured launch/state selections; they do not provide a
shell, arbitrary argv/environment or filesystem API. Vendor payloads require
separate negotiated schema identifiers. Safe failures use closed enums.

The compatibility helper compares Hello to caller-supplied trusted materialization
expectations: complete binding/generation, 32-byte challenge, exact build and
adapter digests. It selects the supported major/minor intersection, rejects
unmet required capabilities and intersects finite limits within ADR-0002 ceilings.
Unknown optional capability names may be ignored; unknown fields in the initial
handshake, duplicate capability names and malformed identities fail closed.
Additive minor support requires explicit implementation and compatibility tests.

Welcome binds the selection to a caller-issued connection ID/epoch. A deterministic
Protobuf digest covers the selected limits, capabilities, immutable evidence and
connection/binding. Challenge and bootstrap proof are excluded from durable
evidence; no credentials or raw handshake frames are logged. Neither the digest
nor the helper is an authentication primitive. Bootstrap consumption, epoch CAS,
authority validation and command acceptance remain trusted transport/application
responsibilities. The independent agentd check uses configured expectations,
never expectations copied from an incoming Welcome.

Source is generated with exact `protoc 3.21.12` and `protoc-gen-go v1.36.12`.
`make generate` writes generated Go; `make verify` rejects drift. The existing
Protobuf runtime becomes a direct dependency, with no new runtime module.
The service descriptor is defined now; gRPC bindings/runtime arrive with AGD-003.

## Consequences

Protocol evolution is independently reviewable and testable before network or
process implementation. Field numbers are stable and must not be reused;
removed fields require reserved numbers/names. A new major uses a new package.
The current schema supplies bounded typed envelopes, not completed handlers for
every declared message. Transport, replay, lifecycle, capture, checkpoint and
adversarial handling remain the corresponding AGD items.

See the [wire reference](../contracts/agentd-protocol.md) and
[AGD-001 evidence](../evidence/agd-001-protocol.md).
