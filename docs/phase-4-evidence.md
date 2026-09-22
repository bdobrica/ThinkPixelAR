# Phase 4 — agentd and sandbox transport

Review date: 2026-09-22. **Not complete.** AGD-001–018 have component-level
implementation/evidence; AGD-020 remains the prerequisite for AGD-019 closure.
The shipped agentd binary still waits in `awaiting_transport` without connecting
or launching a harness. Passing component tests does not establish the Phase 4
exit criterion: an authenticated, controlled harness lifecycle through AR.

## Existing evidence

These links preserve the original verification scope and dates. This review did
not rerun their database, image, cluster or runtime checks.

| Area | Evidence | Qualification boundary |
| --- | --- | --- |
| Protocol and protected startup | [AGD-001](evidence/agd-001-protocol.md), [AGD-002](evidence/agd-002-startup.md) | Versioned handshake and read-only startup; configured is not ready. |
| Authentication and binding isolation | [AGD-003](evidence/agd-003-transport.md), [AGD-005 acceptance](evidence/agd-005-binding-isolation.md) | Real mTLS and separate PostgreSQL composition tests; external policies/providers use fixtures. |
| Process control and capture | [AGD-006](evidence/agd-006-process-control.md), [AGD-007](evidence/agd-007-output-capture.md), [AGD-008](evidence/agd-008-status-heartbeat.md), [AGD-009](evidence/agd-009-signal-interrupt.md) | Real child lifecycle and bounded local observations; no binary command dispatch. |
| Preparation, credentials and shutdown | [AGD-010](evidence/agd-010-checkpoint-preparation.md), [AGD-011](evidence/agd-011-credential-exclusion.md), [AGD-012](evidence/agd-012-shutdown.md) | Candidate preparation, current no-copy behavior, bounded shutdown; no durable checkpoint publication. |
| Rotation/reconnect | [AGD-013](evidence/agd-013-rotation-reconnect.md) | Real mTLS and PostgreSQL boundaries; renewal/stop/provider hooks still require composition. |
| Hostile frames and connection loss | [AGD-014](evidence/agd-014-adversarial-messages.md), [AGD-015](evidence/agd-015-transport-loss.md) | Codec, policy-delivery and liveness checks; durable replay remains unwired. |
| Privileges, controlled harness and authority | [AGD-016](evidence/agd-016-privileges.md), [AGD-017](evidence/agd-017-harness-fixture.md), [AGD-018](evidence/agd-018-report-authority.md) | Local image, process fixture and admission/aggregate regressions; no claim of a production report reducer. |

Implemented decisions remain in [ADRs 0022–0037](adr/README.md). In particular,
[ADR-0027](adr/0027-agentd-admission-composition.md) requires concrete policies,
and [ADR-0037](adr/0037-agentd-rotation-reconnect-recovery.md) explicitly retains
binary composition as a Phase 4 exit requirement. This review adds no decision
and does not reinterpret those accepted records.

## Remaining acceptance work — AGD-020

Complete these existing requirements in one runnable path before closing AGD-019:

- Wire the AR transport listener and agentd client/dispatcher to concrete bounded
  authority and immutable materialization expectations; retain mandatory binding,
  provider, epoch, revocation and deadline checks with no permit-all default.
- Wire the [durable bootstrap delivery coordinator](adr/0038-durable-agentd-bootstrap-delivery.md)
  into materialization/admission and its bounded tenant cleanup pass into a worker.
  The journal/coordinator is implemented; executable scheduling is still pending.
- Implement command/report semantics and durable operation replay reconciliation.
  Contradictory readiness/completion/checkpoint claims must not become canonical
  lifecycle state or authorize retries with ambiguous external effects.
- Multiplex heartbeat/events, command acknowledgements and credential renewal;
  connect disconnect cleanup to local stop/fencing and effective Pod shutdown-budget
  validation, including bounded final reporting.
- Compose current-epoch reconnect and recovery/fallback. When protected in-place
  recovery delivery is unavailable, fail closed and reconcile Sandbox replacement;
  immutable Kubernetes bootstrap projection is not refreshable in place.
- Run the authenticated controlled-process exchange using the
  [AGD-017 fixture](../test/harnessfixture/README.md): start/handshake, capture/status,
  interrupt, transport loss and reconnect with fencing. Record the actual policy,
  persistence and provider configuration, test commands/results and limitations.

Then refresh this document with the integrated evidence, run the applicable
repository/security gates, and commit AGD-019 with its TODO/PLAN closure updates.
Do not substitute separate passing transport and process tests for the exchange.

## RC scope

No paid infrastructure is required. Use the existing homelab/local PC lane under
[ADR-0014](adr/0014-homelab-first-release-candidate.md). Existing
[Phase 3 evidence](phase-3-evidence.md) applies to its qualified artifacts; rerun
relevant qualification for the actual deployed artifact. Future larger amd64,
encrypted snapshot-capable storage and production-scale testing remain described
in [RC infrastructure](operations/rc-infrastructure.md), not newly added Phase 4
prerequisites. Codex, durable workspace resume and platform gateway integrations
remain later phases.

## This review's validation

Source inspection confirmed the dormant path in `internal/app/agentd/run.go` and
its call from `cmd/thinkpixel-agentd/main.go`. Documentation links, repository
hygiene and staged whitespace checks are validated with this evidence commit.
No new runtime or live-provider verification is claimed.
