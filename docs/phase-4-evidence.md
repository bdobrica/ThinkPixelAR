# Phase 4 — agentd and sandbox transport

Review date: 2026-09-23. **Complete for the initial runnable application scope.**
AGD-019 closes the Phase 4 review using AGD-001–018 component evidence and
AGD-020 integrated acceptance (implementation `9531d1b`, closure `64c799c`).
Both binaries now host the authenticated path under [ADR-0042](adr/0042-agentd-binary-hosting-and-homelab-evidence.md).
The [binary hosting evidence](evidence/agd-020-binary-hosting.md) includes local
rotation/reconnect/status verification. Fresh live homelab qualification remains
outstanding and is not implied by application acceptance.

The [integrated application acceptance](evidence/agd-020-integrated-acceptance.md)
now passes as one scenario using the real agentd image, child process, mTLS and
concrete PostgreSQL policies: bootstrap, rotation, handshake/capture/status,
interrupt, disconnect/reconnect, duplicate/stale rejection and cleanup. Kubernetes
readiness and Secret API operations remain explicit external fixtures.

## Closure assessment — AGD-019

The PLAN's controlled-harness exit criterion is met by one integrated exchange,
not inferred from disconnected component tests. AR uses concrete LocalAuthority
admission and PostgreSQL outcomes to start, monitor, interrupt and reconnect to
the real agentd/child process; duplicate and stale commands are rejected. AGD-013
separately covers replacement AR server instances, rotation and durable epochs;
AGD-020 composes rotation and reconnect with the controlled process. The integrated
AR listener runs inside the test process, not the complete AR host executable.

Protocol compatibility, binding isolation, adversarial frames, transport loss,
credential exclusion, privileges and report-authority checks are linked below.
Failure fencing and bounded final reporting are recorded under ADR-0043.
These satisfy the initial application closure selected for AGD-020 without
promoting sandbox reports or provider fixtures into live security evidence.
There is no new architecture decision or change to accepted security requirements.
Phase 5 harness/Codex work is next; deployment qualification and Phase 6 recovery
remain bounded follow-ups listed below.

## Existing evidence

These links preserve the original verification scope and dates. Additional current
validation is recorded in the binary hosting evidence; no live cluster result is
implied by those local checks.

| Area | Evidence | Qualification boundary |
| --- | --- | --- |
| Protocol and protected startup | [AGD-001](evidence/agd-001-protocol.md), [AGD-002](evidence/agd-002-startup.md) | Versioned handshake and read-only startup; configured is not ready. |
| Authentication and binding isolation | [AGD-003](evidence/agd-003-transport.md), [AGD-004](evidence/agd-004-credentials.md), [AGD-005 acceptance](evidence/agd-005-binding-isolation.md) | Real mTLS and separate PostgreSQL composition tests; external policies/providers use fixtures. |
| Process control and capture | [AGD-006](evidence/agd-006-process-control.md), [AGD-007](evidence/agd-007-output-capture.md), [AGD-008](evidence/agd-008-status-heartbeat.md), [AGD-009](evidence/agd-009-signal-interrupt.md) | Real child lifecycle and bounded local observations; no binary command dispatch. |
| Preparation, credentials and shutdown | [AGD-010](evidence/agd-010-checkpoint-preparation.md), [AGD-011](evidence/agd-011-credential-exclusion.md), [AGD-012](evidence/agd-012-shutdown.md) | Candidate preparation, current no-copy behavior, bounded shutdown; no durable checkpoint publication. |
| Rotation/reconnect | [AGD-013](evidence/agd-013-rotation-reconnect.md) | Real mTLS and PostgreSQL boundaries; renewal/stop/provider hooks still require composition. |
| Hostile frames and connection loss | [AGD-014](evidence/agd-014-adversarial-messages.md), [AGD-015](evidence/agd-015-transport-loss.md) | Codec, policy-delivery and liveness checks; durable replay remains unwired. |
| Privileges, controlled harness and authority | [AGD-016](evidence/agd-016-privileges.md), [AGD-017](evidence/agd-017-harness-fixture.md), [AGD-018](evidence/agd-018-report-authority.md) | Local image, process fixture and admission/aggregate regressions; no claim of a production report reducer. |

Implemented decisions remain in [ADRs 0022–0043](adr/README.md). In particular,
[ADR-0027](adr/0027-agentd-admission-composition.md) requires concrete policies,
and [ADR-0037](adr/0037-agentd-rotation-reconnect-recovery.md) explicitly retains
binary composition as a Phase 4 exit requirement. ADR-0042 records the executable
hosting decision without weakening those accepted admission/evidence requirements.

The [ADR-0039 dispatcher exchange](evidence/agd-020-process-dispatch.md) now connects
real mTLS to the structured fixture process, including handshake, capture, status,
duplicate-operation acknowledgement and stop. Its AR authorizer remains test-only;
it does not satisfy executable composition or durable replay acceptance.

[ADR-0040 local policies and replay](evidence/agd-020-admission-replay.md) now
compose with the real admission service and PostgreSQL binding/credential stores.
Concurrent claims, persisted outcomes, revocation and epochs are verified; provider
facts remain fixtures. The optional executable host now joins these policies with the dispatcher;
end-to-end live acceptance remains outstanding.

## Follow-up qualification and recovery work

AGD-020 closure uses the integrated application result above. Retain these
limitations and follow-ups before claiming live qualification or safe replacement:

- Supply the trusted homelab evidence publisher and qualify the actual running
  artifacts, network and mounted workspace. The protected-file reader/verifier is
  implemented; automated host observation collection is not.
- Repeat the passing integrated command-plan/outcome/cleanup/rotation scenario
  against a live homelab materialization with fresh infrastructure evidence.
- Validate connected final stop reporting against that materialization;
  [failure-handling evidence](evidence/agd-020-failure-handling.md) covers local
  authenticated reporting, bounded stopping and expiry-driven fencing/cleanup.
- Implement durable safe-replacement admission after fenced cleanup in Phase 6
  recovery (REC-002–006). Current-epoch
  rejection and expiry-driven recovery work are connected; ambiguous outcomes
  remain pending. Immutable bootstrap projection is not refreshable in place.
- Run the live-provider variant of the passing controlled-process exchange using the
  [AGD-017 fixture](../test/harnessfixture/README.md): start/handshake, capture/status,
  interrupt, transport loss and reconnect with fencing. Record the actual policy,
  persistence and provider configuration, test commands/results and limitations.

AGD-019 closure preserves these qualification and recovery boundaries. It does
not certify a complete Codex demo, live deployment or automatic replacement.

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

Current integrated validation and AGD-020 closure are recorded in
[integrated acceptance evidence](evidence/agd-020-integrated-acceptance.md).
Historical component records above preserve their original scope. No live homelab admission or automatic
physical-evidence publisher is claimed by the binary hosting change.

The 2026-09-23 `make verify` run recorded in AGD-020 closure passed after the final
acceptance-test assertion was added. AGD-019 changes only documentation, so that
aggregate result is reused; it is not claimed as a new test run. The opt-in
Docker/PostgreSQL acceptance also retains its separately recorded run. Closure
checks: repository hygiene and whitespace checks on the changed files. No live
cluster checks were run for this review. Existing unrelated working-tree edits
are excluded from the closure commit.
