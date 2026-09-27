# E2E-001 — live standalone continuation scenario

On 2026-09-27, `TestStandaloneKubernetesContinuation` passed in **89.20 seconds**
against real PostgreSQL and two controller-created ARM64 Kata Agent Sandboxes.
The [operator reproduction](../../test/e2e/standalone/README.md) builds the guest
test binary and runs the joined scenario from HTTP admission through a second
completed Execution. This is a composed integration test, not the executable
standalone MVP gate.

## Observed identities

| Object | Observed value |
| --- | --- |
| Namespace | `ar-e2e001-20260927155104-1250501` |
| Session | `01a0e390-1b1c-7939-b0c5-776536c63066` |
| Workspace | `01a0e390-1b1c-7b94-99e5-549ab1055c8c` |
| First Execution | `01a0e390-1b60-7c03-99c7-2d766f557fd6` |
| Second Execution | `01a0e391-42fa-7f5d-af05-e991f6ed9176` |
| Checkpoint | `01a0e390-a510-7a5d-8660-4f07b5fc1dd0` |
| Codex thread, before and after | `01a0e390-86b1-7813-ae16-b5b1c9007855` |
| Old Sandbox UID | `fe06b44b-bfe8-4837-82e0-9e4b158f9790` |
| Old Pod UID | `1d5cdd28-afd2-48f8-bdc4-2709bcacd168` |
| Replacement Sandbox UID | `8eb16ea0-6028-4b03-af07-f80d1195112c` |
| Replacement Pod UID | `4106425b-2993-437a-b014-12f2fb7eebf5` |
| Worker / runtime | `k3spi-02` / `kata-qemu-runtime-rs-ar331` |
| Probe OCI index | `sha256:710d3a27a8c6e99ee70e2172b069cc4ecbad506a06ba08939227a1a5e9504ca4` |

Both guests passed the existing trusted-worker QEMU/KVM proof, including pinned
QEMU/kernel/guest image hashes and correlation with the exact live Pod UID.
The guest verified Codex 0.155.0's ARM64 executable checksum. Each actual turn
used the agentd process supervisor and the real Codex protocol driver.

## What passed

- Real TCP HTTP Session creation and two separate Execution admissions using
  production application services, PostgreSQL, and LocalAuthority.
- First turn completion, process reaping and ephemeral-home removal; cancellation
  of the first grant before suspension.
- Fsynced bounded export of the Workspace file, rollout and restore metadata
  outside the first Sandbox. Both vendor objects are digest-bound in the signed
  manifest. The old-home credential canary is absent from the export.
- Production Workspace generation publication, Ed25519 checkpoint publication,
  integrity validation and Session suspension. Admission while suspended returns
  HTTP 409.
- Durable release authorization followed by actual Sandbox deletion and confirmed
  absence of both the original Sandbox and its Pod.
- Production resume reservation/publication with a live test materializer. The
  replacement Sandbox, Pod and both PVCs have different UIDs. Restoration reads
  only the exported files into the fresh volumes, not the old PVCs or home.
- Exact thread readiness without a model request; idempotent resume replay;
  second Execution generation 2 and a distinct local grant. The resumed model
  request includes the first conversation, the Workspace content is unchanged,
  and the second Execution's HTTP status is `SUCCEEDED`.
- Two completed Executions, two local grants, and unchanged checkpoint objects.

The disposable database applied all 33 current migrations. Focused HTTP boundary
and agentd restoration tests were also run with `-race`; `go vet` covered both
changed packages. Shell syntax validation passed for the runner. Ordinary tests
skip the opt-in live scenario and guest; the explicit runner requires its inputs.

Two earlier runs exposed and corrected test composition errors: missing profile
resolution persistence and slash-containing checkpoint object references rejected
by the published identifier schema. Their namespaces were removed. No production
contract was relaxed to make the test pass.

## Limits

Authentication, qualification, lifecycle materialization/terminalization dispatch,
file-export storage, readiness callbacks and Kubernetes exec command delivery are
test fixtures. In particular, SQL fixture transitions are not an implementation
of the missing production lifecycle workers. The production server executable is
not started by this test, and authenticated agentd mTLS delivery is not exercised.
No synthetic database Execution or grant is created during resume readiness.

Model responses are loopback fixtures; AG, LLMGW, TG and provider credentials are
not involved. Fresh grant identity and clean guest state do not establish gateway
revocation or arbitrary credential detection in untrusted content.

The homelab has no CSI snapshot driver. Separate `local-path` PVCs and bounded
controller-side export demonstrate complete compute replacement, not CSI,
node-loss recovery, production storage isolation/quotas, retention, full server
restart recovery, or safe retry of every ambiguous provider outcome. The export
and signing key have test lifetime only. These limitations leave the corresponding
SES/WSP/REC tasks and MVP-001 open; this evidence does not promote the repository
to a usable standalone release.
