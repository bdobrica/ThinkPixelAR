# Agentd v1 wire protocol

Source: [agentd.proto](../../api/agentd/v1/agentd.proto).
Decision: [ADR-0022](../adr/0022-agentd-protocol-handshake.md), implementing the
transport shape of [ADR-0002](../adr/0002-agentd-outbound-mtls-grpc-transport.md).

## Compatibility exchange

The agentd-initiated bidirectional `AgentTransport.Connect` stream carries
`Envelope` frames. Before application traffic, agentd sends Hello and AR replies
with Welcome after authentication and persisted binding checks. Initial handshake
frames use the Hello/Welcome body; application sequence/connection headers become
mandatory after Welcome. Handshake payload identity never selects a tenant or
binding. No harness launches merely because a Hello is compatible.

The implemented compatibility helper accepts independently supplied expectations
and a freshly allocated connection ID/epoch. It requires canonical AR UUIDv7
identifiers, positive generation/epoch, exact immutable SHA-256 build and adapter
references, a matching 32-byte challenge, protocol major 1 with minor 0 in range,
and compatible finite limits. Bootstrap proof, when present, is exactly 32 bytes;
its authenticity, expiry and one-time consumption belong to transport acceptance.
Reconnect may omit bootstrap proof only when the transport validates its current
session credential. Compatibility validation does not grant that exception.

Capability identifiers are unique lowercase tokens, at most 64 ASCII characters,
with at most 32 supported and 32 required entries. Required entries must be offered
locally and supported by the peer. Optional unsupported entries are ignored. The
selected intersection is sorted. Trusted callers supply only implemented vocabulary
and include every capability needed by the chosen execution path as required.
The schema alone does not advertise any command handler as implemented.

All limits are positive and no larger than ADR-0002's ceilings. Selection takes
the smaller value for each bound; inconsistent combinations fail. Payload bounds
leave envelope headroom, diagnostics fit within event bounds, and the liveness
window is at least twice the heartbeat interval. Limits may only narrow the
execution profile's separately enforced physical envelope.

Welcome includes the complete selected binding, build/adapter references, limits,
capabilities and connection ID/epoch. Its `negotiation_digest` is SHA-256 over
Go deterministic Protobuf serialization of Welcome with `challenge` and
`negotiation_digest` cleared. Bootstrap proof is never included. The message has
no maps or unknown fields. This versioned algorithm is compatibility evidence,
not a cross-language claim of generic canonical Protobuf. Agentd verifies the
result against independently configured expectations and its own Hello; TLS and
trusted epoch checks remain mandatory. Persist selected non-secret fields and
digest, never the raw Hello/Welcome or rotation payload.

## Envelope and evolution

Application frames carry explicit version, connection/epoch, UUID message ID,
per-direction sequence, full binding, applicable harness/operation/digest and
bounded timestamp/deadline metadata. The oneof is the closed message kind.
Commands, candidate observations, acknowledgements/credit, heartbeats, rotation
and safe failures have separate typed bodies. Inline payload and artifact
reference are mutually exclusive; adapter payload schema names are negotiated.
These rules are handler/transport obligations implemented by later AGD items;
AGD-001 validates the compatibility exchange only.

Rotation material is ephemeral secret data. Observations, heartbeat process state,
checkpoint preparation and acknowledgements are sandbox reports, never
canonical Session/Execution state or evidence of external side effects.

Unknown major versions, unknown handshake fields (including nested fields),
unknown required capabilities and malformed ranges are rejected. Minor additions
need explicit negotiation and tests; protobuf's unknown-field preservation is not
permission to execute an unknown command. Do not reuse field numbers or enum
values. Reserve removed fields and names; breaking evolution uses a new package.

## Generation and checks

Install `protoc 3.21.12`, then use `make generate`. The script obtains the exact
`protoc-gen-go v1.36.12` from the existing pinned module using a temporary binary
directory, removes it afterward, and never vendors tool executables. Generated
files are not hand edited. `make agentd-protocol-check` regenerates into a temporary
directory and compares output; it is included in `make verify` and CI.

Focused checks:

```sh
go test -race ./api/agentd/v1 ./internal/adapters/sandboxtransport/protocol
go test ./internal/adapters/sandboxtransport/protocol -run '^$' -fuzz FuzzHandshake -fuzztime 5s -parallel 2
```
