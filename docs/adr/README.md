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

- [ADR-0005: Kubernetes client boundary](0005-kubernetes-client-boundary.md)
- [ADR-0006: Agent Sandbox v1 API pin](0006-agent-sandbox-v1-api-pin.md)
- [ADR-0007: Runtime Profile loading](0007-runtime-profile-loading.md)
- [ADR-0008: Kata homelab substrate](0008-kata-homelab-substrate.md) — Accepted
- [ADR-0009: Coding template mapping](0009-coding-template-mapping.md) — Accepted
- [ADR-0010: Durable sandbox acquisition](0010-durable-sandbox-acquisition.md) — Accepted
- [ADR-0011: Workspace attachment seam](0011-workspace-attachment-seam.md) — Accepted
- [ADR-0012: Operator Kata runtime mapping](0012-operator-kata-runtime-mapping.md) — Accepted
- [ADR-0013: Effective sandbox security](0013-effective-sandbox-security.md) — Accepted
- [ADR-0014: Homelab-first release candidate](0014-homelab-first-release-candidate.md) — Accepted
- [ADR-0015: Sandbox network enforcement](0015-sandbox-network-enforcement.md) — Accepted
- [ADR-0016: Compute reconciliation](0016-compute-reconciliation.md) — Accepted
- [ADR-0017: Durable compute monitoring](0017-durable-compute-monitoring.md) — Accepted
- [ADR-0018: Compute loss recovery](0018-compute-loss-recovery.md) — Accepted
- [ADR-0019: Runtime-enforced process ceiling](0019-runtime-enforced-process-ceiling.md) — Accepted
- [ADR-0020: Bounded ephemeral scratch](0020-bounded-ephemeral-scratch.md) — Accepted
- [ADR-0021: Sandbox capability discovery](0021-sandbox-capability-discovery.md) — Accepted
- [ADR-0022: Agentd protocol and compatibility handshake](0022-agentd-protocol-handshake.md) — Accepted
- [ADR-0023: Agentd read-only bootstrap and process startup](0023-agentd-readonly-bootstrap.md) — Accepted
- [ADR-0024: Agentd authenticated gRPC channel](0024-agentd-authenticated-grpc-channel.md) — Accepted
- [ADR-0025: Agentd credential issuance and renewal](0025-agentd-credential-issuance.md) — Accepted
- [ADR-0026: Durable agentd credential registry](0026-durable-agentd-credential-registry.md) — Accepted
- [ADR-0027: Agentd admission composition](0027-agentd-admission-composition.md) — Accepted
