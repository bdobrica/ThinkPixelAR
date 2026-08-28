# Architecture Decision Records

Architecture Decision Records (ADRs) capture decisions that constrain ThinkPixelAR's implementation or operation.

## Lifecycle

1. Copy `template.md` to a zero-padded sequential filename.
2. Set status to `Proposed` and open the decision for review.
3. Set status to `Accepted` only after alternatives, security, operations, and compatibility effects have been evaluated.
4. Do not rewrite an accepted decision to change its meaning. Create a new ADR with status `Supersedes ADR-NNNN`, and mark the old record `Superseded by ADR-NNNN`.
5. Use `Rejected` for a proposal that was considered but not selected and `Deprecated` when a decision no longer applies without a direct replacement.

The allowed base statuses are `Proposed`, `Accepted`, `Rejected`, `Deprecated`, and `Superseded`.

## Index

- [ADR-0001: Bind each Session to one immutable agent runtime](0001-immutable-session-runtime-binding.md) — Accepted
- [ADR-0002: Use outbound mTLS gRPC for the initial agentd transport](0002-agentd-outbound-mtls-grpc-transport.md) — Accepted
- [ADR-0003: Gate Session fork on qualified storage and adapter capabilities](0003-capability-gated-session-fork.md) — Accepted
- [ADR-0004: Exclude a durable workflow engine from MVP and RC](0004-no-temporal-in-mvp-rc.md) — Accepted
