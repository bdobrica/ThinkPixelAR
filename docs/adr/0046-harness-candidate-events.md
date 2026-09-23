# ADR-0046: Closed harness candidate events before trusted publication

Status: Accepted 2026-09-23

## Decision

HNS-004 gives the existing 21 normalized HarnessAdapter candidate types a closed
Go registry, `thinkpixel.harness-event/v1` schema name and typed payload families.
All initial candidates are Confidential and capability-gated. The declaration
check rejects unknown types/schema, lowered classification and unnegotiated event
capabilities. It does not claim payload, replay or authority validation.

Message/tool/local-approval candidates name possible projections into existing
Runtime Event types after trusted validation. Lifecycle, process, usage and
checkpoint-preparation candidates have no direct Runtime Event mapping. In
particular a turn-completion observation cannot directly publish Execution success,
and local approval cannot authorize a governed tool call. AR remains responsible
for current-fence/authority checks, canonical transitions and durable publication.

Payload shapes contain only selected user-visible fields, stable AR identities,
bounded operational metadata and protected references. No vendor-object passthrough
or reasoning event is registered. Concrete vendor mapping, payload validation and
redaction remain adapter/ingestor responsibilities, tested with the actual protocol.

## Scope

This implements the candidate seam in ADR-0044 and the existing HarnessAdapter /
Runtime Event contracts; no public event enum, API, protobuf, database schema or
dependency changes. The HNS-003 suite now checks these declarations rather than
private fixture event names. Its synthetic observations are not a qualified Codex
mapping. Durable correlation/deduplication and the Session event publisher remain
their existing later-phase work.

Proceed to pinned Codex startup/thread/turn/events/interrupt and the Kata demo;
do not add a generic event bus or adapter ecosystem as a prerequisite.

See [candidate-event mapping rules](../contracts/harness-events.md).
