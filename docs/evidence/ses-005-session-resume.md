# SES-005 replacement Session resume

Completed 2026-09-28 for the bounded standalone operator lane.

The [resume executable](../../cmd/thinkpixel-session-resume/README.md) now composes
current operator approval, signed checkpoint validation, protected immutable file
exports, PostgreSQL lifecycle coordination, concrete Kubernetes materialization,
independent readiness and failed-candidate cleanup. It needs no test callback to
resume an existing Session. General HTTP lifecycle composition remains separate.

The supported lane is the exact `coding-homelab-arm64` profile, pinned Codex
0.155.0/image, worker `k3spi-02`, bounded Kata handler, paired local-path PVCs and
fresh 32 MiB Pod-owned scratch. The export contains one Workspace file plus the
Codex rollout and restore metadata. It does not claim arbitrary repository export,
CSI snapshots, node-loss recovery or the full standalone/integrated MVP.

## Behavior verified

- The existing durable journal reserves exact checkpoint/runtime/head and fresh
  candidate/bootstrap/attachment identities without admitting an Execution.
- Current protected approval is reread; JSONB runtime serialization is compared
  canonically. Missing/corrupt exports and changed/expired/revoked approval fail
  closed. Local grants must be retired before materialization.
- Candidate mutations hold a bounded Session fence, serializing creation against
  failure/close/cleanup. Lost responses retain deterministic owned resources.
- Readiness rereads exact Pod/PVC/PV identities, effective Pod settings, independent
  worker QEMU/KVM/artifact proof and physically bounded scratch. The guest restores
  only fixed paths, resumes the exact thread, stops the child and removes its fresh
  home. Its infrastructure-only client cannot start a turn or switch to ordinary
  execution mode. The later Execution gets its own model route and supervisor.
- Materialization and independent readiness each retain a one-minute bound; the
  coordinator permits two minutes for both. Transient readiness unavailability
  retains the candidate instead of publishing a false integrity failure.
- A fresh active local grant and current Execution/Attempt fence are required to
  claim the committed attachment/provider UID. A substituted Workspace PVC is
  rejected. Resume replay neither reallocates nor issues authority.
- Abandoned-candidate cleanup uses UID-preconditioned foreground deletion and
  proves Sandbox/Pod absence. Replay is safe; PVCs and exports remain unchanged.

## Executed evidence

The updated [live scenario](../../test/e2e/standalone/README.md) passed in **140.30s**
using the real resume executable, including a second invocation after publication.
It then passed E2E-001, E2E-002 and E2E-003: fresh compute, preserved Workspace and
conversation, fresh grants/keys/proofs, and retired authority rejected before expiry.

- Session: `01a0e931-084e-7159-91c3-af087e52ca04`
- Checkpoint: `01a0e931-979e-7d49-932f-3b5aa6e2a0a2`
- Old Pod: `7976d8d9-3fee-4067-b2eb-598a4970151f`
- Replacement Pod: `0a806f1d-630e-4bf2-b80e-6a9c20306cb3`
- Executions: `01a0e931-088a-7be5-855b-677d58c83759`,
  `01a0e932-f40f-7a06-a502-936f57dcdf97` (generations 1 and 2)
- Guest image:
  `docker.io/library/thinkpixel-codex@sha256:93728a00d7b9245ab5e7a3e837040150fe12795fe0f3a65b24d57bcfb46db7e6`

`TestStandaloneKubernetesAbandonedResumeCleanup` passed on an earlier failed
candidate in **11.81s**, including cleanup replay and PVC/export preservation.
The real pinned AMD64 Codex regression passed in **2.30s**, proving infrastructure
restoration without the old custom model-provider configuration, followed by a
normal second turn with conversation continuity.

Race-enabled affected supervisor/Codex/Session/adapter tests passed. PostgreSQL
`TestSessionResume*` and `TestSandboxBindings*` passed against a disposable database
with all **33 existing migrations**. No new migration is required. The tests cover
creation/failure serialization, stale candidate rejection, transient readiness,
restart/replay and existing binding RLS/fences. Affected `go vet`, ARM64 probe/test
cross-compilation, scoped whitespace and documentation-link checks also passed.

Earlier live attempts exposed canonical JSONB comparison, unavailable scratch
capacity and old model-provider restoration issues; these were corrected before
the passing run. The original coordinator-only evidence remains in Git history.

The scenario still uses fixture authentication, checkpoint preparation, ordinary
Execution dispatch and a loopback model. It is not AG/LLMGW/TG or live agentd mTLS
qualification. SES-007 and REC-004 remain open. The four disposable test namespaces and PostgreSQL container were removed. Two
consumed 32 MiB scratch slots are retained under the existing storage policy;
they are not reset or reused.
