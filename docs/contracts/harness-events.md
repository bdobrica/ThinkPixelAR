# Normalized harness candidate events v1

Status: Normative application-port mapping contract; HNS-004.

`HarnessEvent.SchemaVersion` and `Content.Schema` are
`thinkpixel.harness-event/v1`. These are untrusted adapter candidates, distinct
from persisted `thinkpixel.runtime-event/v1` envelopes. The existing public Runtime
Event registry is unchanged. Go names, payload shapes and declaration checks live
in `internal/ports/harness/events.go`; [ADR-0046](../adr/0046-harness-candidate-events.md)
records the implemented boundary.

## Closed registry and mapping

All events require `structured-events` and the additional capability below, if
any. Every candidate in this initial lane is `Confidential`. Only trusted AR
policy may subsequently choose publication/classification; an adapter cannot
lower classification. The outer stream/fence/operation/identity fields retain the
[HarnessAdapter contract](harness-adapter.md).

| Candidate types | Payload family | Additional capability | Possible Runtime Event projection after validation |
| --- | --- | --- | --- |
| `harness.started`, `harness.ready`, `harness.exit-observed` | observation | — | None directly; process observations only. |
| `execution.started`, `execution.progress`, `execution.completion-observed`, `execution.failure-observed` | observation | — | None directly; AR decides lifecycle under current authority/fence. |
| `message.delta` | message-delta | — | `assistant.message.delta` |
| `message.completed` | message-completed | — | `assistant.message.completed` |
| `process.started`, `process.output`, `process.completed` | process | `structured-process-events` | None directly; protected output references remain observations. |
| `tool.requested` | tool | `structured-tool-events` | `tool.requested`, never authorization to invoke. |
| `tool.started`, `tool.completed`, `tool.failed` | tool | `structured-tool-events` | `tool.status_changed`, with observed phase derived from the candidate type. |
| `approval.requested`, `approval.resolved` | approval | `local-approval-events` | `permission.requested`, `permission.resolved`; harness-local permission only. |
| `checkpoint.prepare.started`, `checkpoint.prepare.completed` | checkpoint | `checkpoint-prepare` | None directly; never `checkpoint.committed`. |
| `usage.observed` | usage | `usage-observation` | None directly; not authoritative accounting. |

`EventRuleFor` returns this table. An empty RuntimeType means no direct projection.
A nonempty value is a mapping declaration, not an event publisher or state change.
Even `execution.started`, whose name exists in both registries, cannot be copied
into the public stream as an authoritative lifecycle event. `execution.completed`,
`execution.failed`, `checkpoint.committed` and `artifact.published` are not valid
candidate names. Unknown names and schemas fail closed; vendor extensions require
an explicit reviewed mapping before use in this lane.

## Payload families

Payloads are closed JSON objects matching the exported Go shapes. Omitted optional
fields have no implicit authority or default success. UUID fields are AR-assigned
UUIDv7 identities; vendor identifiers remain adapter mapping data. A payload is
either bounded inline JSON or a protected reference in the Content envelope, never
both. Inline data is capped by the negotiated event limit and 64 KiB; reference
strings by 2048 bytes and the trusted reference policy. Oversize data needs an
authorized artifact path or explicit failure, never silent truncation.

| Family | Fields and rules |
| --- | --- |
| observation | Optional `reason_code`, `result_reference`. Reasons are `completed`, `cancelled`, `interrupted`, `failed`, `crashed`, `unknown`; absent means no reason observation. Never include raw vendor errors. |
| message-delta | Required `message_id`, `text`. Text is explicitly user-visible assistant content that passed data policy. |
| message-completed | Required `message_id`, `first_sequence`, `last_sequence`; optional `content_reference`. The range identifies this message's observed deltas in the operation stream. Both zero require a complete protected content reference; otherwise first is positive and last is at least first. No invented deltas or content on gaps. |
| process | Required `process_id`; optional `output_reference` for output, `exit_code` for completion. Exit 0 never means Execution success. Commands/environment/raw stdout are not extra fields. |
| tool | Required `tool_call_id`; optional `content_reference` for protected arguments/results. Status is derived from event type; no credential, approval token or generic provider response field. |
| approval | Required `approval_id`; optional `content_reference`. Resolved events require `decision` equal to `allow` or `deny`; requested events omit it. This is never enterprise authorization. |
| checkpoint | Required `preparation_id`. Paths/manifests remain on the checkpoint preparation port; no durable status is inferred. |
| usage | Required nonnegative integer `input_tokens`, `output_tokens`. These are vendor observations, not AG budget settlement. |

The Go structs are definitions, not decoders or security filters. The concrete
adapter/ingestor must reject unknown/duplicate keys, invalid UTF-8, disallowed
controls, malformed field combinations, invalid IDs and excessive size/depth before
use, and enforce data policy at publication. `CheckEventDeclaration` checks only
registered type, matching schema, Confidential classification and negotiated
capabilities. It does not validate payloads or establish current authority.

## Adapter mapping procedure

1. Bound and decode the vendor frame using its pinned protocol. Select explicit
   registered fields into typed payloads; never serialize a vendor object wholesale.
2. Drop hidden reasoning, scratchpad and private deliberation fields before event
   allocation. A reasoning-labelled field is not user-visible merely because it
   arrived in a stream. No reasoning type or catch-all extension payload exists.
3. Apply the configured redaction/classification policy to user-visible content
   and referenced artifacts. Credentials and raw vendor diagnostics cannot be
   copied to payloads/logs. Without an appropriate policy, suppress content or fail.
4. Bind each event to the current immutable handle/fence and Execute operation.
   Allocate stable event/stream identities and consecutive per-operation sequence;
   replay retains identity/content. Conflicting duplicates, gaps, regressions and
   exhaustion fail the stream. Do not use vendor order/IDs as Session sequencing.
5. Return candidates with bounded backpressure. AR rechecks the current binding,
   authority, operation and replay state before deciding lifecycle or projecting
   user-visible events. AR allocates durable Session sequence/identity and commits
   state/event/outbox atomically. Candidates never bypass those checks.

The next Codex step implements the pinned vendor-to-candidate mappings and their
reasoning/credential-canary tests. This definition does not claim those mappings,
payload validators, a public SSE publisher or a real Codex/Kata execution exist.
