# E2E-002 — continuity after complete compute replacement

The [standalone live scenario](../../test/e2e/standalone/README.md) now checks
continuity explicitly in the existing E2E-001 path, without another orchestration
implementation or production contract change.

## Assertions

- The first supervised Codex turn receives a unique assistant marker derived from
  its Execution ID. The second model request must contain that exact marker in
  structured assistant history, not merely somewhere in the request body. The
  second response uses its own Execution marker; user input never includes the
  old marker. A focused test rejects user/tool messages as continuity evidence.
- After the first turn, the guest fixture edits and fsyncs the Workspace file with
  the first Execution ID. The external export, resumed readiness and second turn
  must preserve its exact bytes/SHA-256. This is a fixture edit, not a Codex tool
  call. The signed checkpoint binds the exported Workspace and vendor state.
- After authorized compute release, the old Sandbox and Pod are confirmed absent.
  Both exact old fixture PVCs are also deleted before replacement starts. Only
  the external bounded checkpoint export supplies the new volumes. PVC deletion
  does not attest physical-media erasure or implement production Workspace cleanup.
- Replacement requires different Sandbox, Pod and both PVC UIDs, with independent
  QEMU/KVM host proof. Actual second-execution Attempt and Sandbox binding IDs are
  fresh; all guest-result binding fields must match the requested identity.
- The resume reservation and final PostgreSQL records retain the same Session,
  Workspace, checkpoint and Workspace-generation identities. Workspace generation
  remains 1; Session execution generation advances from 1 to 2 only at admission.
  The final Session is IDLE and the second Execution is HTTP-visible SUCCEEDED.
- The exact Codex thread survives, readiness performs no model call, and resume
  replay preserves its result. Exported checkpoint objects remain unchanged.

## Live verification — 2026-09-27

`TestStandaloneKubernetesContinuation` passed in **91.92 seconds**, printing both
`E2E-001 LIVE PASS` and `E2E-002 LIVE PASS`. All 33 migrations were applied to a
fresh disposable PostgreSQL database. Both guests ran pinned Codex 0.155.0 on
`k3spi-02` using `kata-qemu-runtime-rs-ar331`; both passed trusted-worker host proof.

| Identity | Observed value |
| --- | --- |
| Namespace | `ar-e2e001-20260927160631-1265892` |
| Session | `01a0e39e-3ece-7c5a-bf40-43de4a510404` |
| Workspace | `01a0e39e-3ece-780f-b6a8-0c8659ccb282` |
| Workspace generation | `01a0e39e-ca79-7d4e-96c3-a4750c47fd93` |
| Checkpoint | `01a0e39e-cab5-7e68-94f6-4b7978231009` |
| First Execution | `01a0e39e-3f15-70c3-b3c5-aa27d7dc96aa` |
| Second Execution | `01a0e39f-7204-7d1c-a7af-7393f56a39d2` |
| Codex thread before/after | `01a0e39e-ad90-70f2-a03f-6eef2d1a08d6` |
| Old Sandbox / Pod UID | `cc97230a-1491-48b9-af9b-73506e57027a` / `f9d2f624-dd7b-4960-b765-a21d4911f29e` |
| New Sandbox / Pod UID | `3d525a36-4d47-4bf4-927f-71ed793a1975` / `8477bde1-0031-4e5d-94b7-50aaade7fa88` |
| Deleted old Workspace / state PVC UID | `7deca6eb-48e8-4e9d-8cd6-231d784493a4` / `67116ec3-6ad0-4a2f-9f45-5f4651d325cb` |
| Workspace SHA-256 | `4651b823d2147640ce07a14b2de976bc5a54ad54a757fb37df328a77a95d2be2` |
| Probe OCI index | `sha256:a66d1a419f7bf0e68bea58922b33f2f759c93dcbde4dc1c8daea147e1a754a95` |

Focused checks passed, including `TestContinuationAssistantMarker` with `-race`,
and `go vet` for the HTTP and agentd packages. Ordinary runs skip the opt-in live
scenario; the separate operator run above exercised it.

## Limits

The existing [E2E-001 fixture boundaries](e2e-001-live-continuation.md#limits)
still apply: local authority, loopback model, test authentication, lifecycle SQL,
bounded file export, readiness callbacks and Kubernetes exec transport. This
proves the composed continuation path, not executable MVP wiring, AG/LLMGW/TG
integration, CSI/node-loss qualification or full controller restart recovery.
