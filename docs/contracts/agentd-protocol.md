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
`protoc-gen-go v1.36.12` and `protoc-gen-go-grpc v1.6.2` from their pinned modules
using a temporary binary directory, removes it afterward, and never vendors tool executables. Generated
files are not hand edited. `make agentd-protocol-check` regenerates into a temporary
directory and compares output; it is included in `make verify` and CI.

Focused checks:

```sh
go test -race ./api/agentd/v1 ./internal/adapters/sandboxtransport/protocol
go test ./internal/adapters/sandboxtransport/protocol -run '^$' -fuzz FuzzHandshake -fuzztime 5s -parallel 2
```

## Authenticated adapter

AGD-003 supplies generated gRPC bindings and the authenticated channel described
in [ADR-0024](../adr/0024-agentd-authenticated-grpc-channel.md). Its mandatory
trusted admission port resolves by certificate identity and verifies bootstrap,
current authority and durable epoch. Frame checks run before delivery. The initial
channel accepts one-time bootstrap or current-credential reconnect. The optional
`rotation.v1` capability enables the existing Rotation REQUEST/ISSUED messages;
REQUEST carries no key/certificate/expiry, and ISSUED is bounded to 16 KiB
certificate PEM, 4 KiB key PEM and a maximum 15-minute lifetime. Issuance and
epoch persistence live in trusted application/registry code, not TLS itself. The
[runbook](../operations/agentd-transport.md) records exact bounds and composition.

## Optional bounded process control

`process-control.v1` registers START, STOP, RESTART, INTERRUPT and STATUS with
`payload_schema` equal to the capability name, empty payload and no artifact
reference. Commands require a configuration digest, UUID HarnessHandle/operation,
request digest and finite unexpired deadline. Configuration/request digest encoding,
local replay bounds and process correlation are specified in
[ADR-0039](../adr/0039-agentd-process-control-dispatch.md). Unsupported commands fail
before process invocation. INTERRUPT in this capability means bounded stop.

Sanitized stdout and stderr use DIAGNOSTIC observations with schemas
`process-control.v1/stdout` and `process-control.v1/stderr`. Sanitized adapter records
use CANDIDATE_EVENT and `process-control.v1/event`. Payloads remain bounded ephemeral
bytes, never authoritative lifecycle or canonical Runtime Events. The default
sanitizer replaces unclassified content with `[REDACTED]`.

Acknowledgement repeats the command message ID, request digest and accepted
sequence, with matching envelope operation/handle. A repeated operation can return
its earlier acknowledgement even after the process has stopped; Heartbeat describes
current local health. Failures use OUTCOME_UNKNOWN without reflected payloads.
Capabilities absent from negotiation cannot activate this dispatcher. The schema
addition is optional and changes no Protobuf field, version or generated artifact.
The shipped binaries compose this capability under
[ADR-0042](../adr/0042-agentd-binary-hosting-and-homelab-evidence.md).
[AGD-020 application acceptance](../evidence/agd-020-integrated-acceptance.md)
records the tested scope and remaining live-provider qualification.

## Local durable dispatch policy

The initial local policy requires explicit local mode, issuer `thinkpixelar/local`,
and immutable trusted materialization tied to an already admitted Execution.
[ADR-0040](../adr/0040-local-agentd-admission-and-durable-dispatch.md) defines the
PostgreSQL authority/revocation, sequence and command journal implementation.
It implements the existing mandatory admission/frame ports; provider verification
and credential checks remain in agentdadmission.Service.

A command's durable claim commits before Send. A retry reads CommandOutcomes;
it never sends that operation again. PENDING is ambiguous and UNKNOWN is a reported
uncertain outcome; either blocks new commands on the sandbox until trusted recovery.
ACKNOWLEDGED is only a correlated transport outcome, never Execution completion.
The 128-command lifetime budget and single outstanding command include STATUS.

## Optional Codex thread creation

`codex-thread.v1` extends `process-control.v1` START for the pinned
`codex-app-server` adapter. It must be supported **and required** on both peers;
without it the existing initialization-only behavior is preserved. START sends
one `thread/start` with the configured working directory, `approvalPolicy: never`,
`sandbox: read-only`, and `ephemeral: false`. Both the matching response and
`thread/started` must confirm the same vendor identity. Cancellation or ambiguous
outcomes never trigger a second creation request.

Before START acknowledgement, agentd sends PROCESS_STATUS with schema
`codex-thread.v1`, no artifact reference, and exactly this canonical JSON shape
(field order as shown, no whitespace or extra keys, maximum 256 bytes):

```json
{"process_id":"<AR UUIDv7>","thread_id":"<lowercase vendor UUID>"}
```

The envelope repeats the START operation, request digest and HarnessHandle. These
are candidate identity observations. The local durable policy validates current
Attempt/generation, live authority, connection epoch and the committed START claim
before inserting immutable HarnessBinding metadata and linking the Attempt in one
transaction. Adapter/build and harness negotiation pins come from trusted
materialization, never the observation. Identical reports are idempotent;
conflicting identities fail closed. START ACK is rejected until this record exists.
No Session/Execution lifecycle state or authority is granted by the report.

RESTART and a new START for a Session with a saved HarnessBinding are rejected in
this mode until resume is implemented. The tenant-scoped binding reader supplies
identity for continuation/checkpoint composition; it neither authorizes access nor
proves that vendor files have been checkpointed. Homes remain ephemeral under
ADR-0050. No Protobuf field or generated artifact changes.

## Optional Codex turn start

`codex-turn.v1` registers EXECUTE for the pinned Codex driver. Both peers must
support and require it together with `codex-thread.v1` and `process-control.v1`.
Existing process-control commands retain their original digest/shape. EXECUTE
uses `payload_schema: codex-turn.v1`, no artifact reference, and canonical JSON:

```json
{"input_id":"<AR UUIDv7>","classification":"Confidential","text":"Execution input"}
```

Field order and encoding are Go `encoding/json` output for the typed payload.
Extra/duplicate fields, noncanonical encoding, invalid UTF-8 and empty text are
rejected. Text is bounded to 16 KiB; the escaped payload to 100 KiB and the selected
transport command limit. Classifications are Public, Internal or Confidential.
`agentdserver.ExecutionCommand` maps `harness.ExecuteRequest` with matching handle
fence and `text/plain` inline content. Artifact references and nonempty options
fail explicitly; callers cannot set a model, cwd, environment, approval or sandbox
policy through this input.

The request digest is SHA-256 of the UTF-8 bytes:
`codex-turn.v1 + NUL + configuration_digest + NUL + harness_handle + NUL`, followed
by the canonical payload bytes, encoded as lowercase hex with `sha256:` prefix.
Trusted materialization pins `ExecuteOperationID` and `ExecuteDigest` for this
Execution before registration. The snapshot and command journal retain these
identities/digests, never prompt text. Input is supplied transiently by the trusted
plan; these pins do not grant authority.

Dispatch requires a persisted HarnessBinding for the current Execution/Attempt,
its acknowledged START, live authority and current transport epoch. The existing
durable claim commits before Send; ambiguous claims cannot be resent. Agentd
reserves the operation before sending `turn/start` on the current thread. It maps
text to one Codex text item, preserves the configured cwd and explicitly retains
`approvalPolicy: never` and a read-only, no-network sandbox policy. The driver
accepts only the matching bounded response with an in-progress vendor turn ID.
It keeps that ID private for subsequent adapter work. Same-operation replay
returns the cached result; changed input or a second operation cannot start a
second turn. Failures/cancellation close the protocol and stop the child.

EXECUTE acknowledgement means turn-start acceptance, not Execution completion or
model success. The first-turn lane allows one operation per process; streamed
events, completion, protocol interrupt and continuation retain their separate
implementation tasks. Until event consumption exists, protocol output remains
private under bounded pipe backpressure. No new provider credentials or model
route are introduced. The local-model fixture in the evidence is test-only.
