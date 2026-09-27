# E2E-003: Fresh local authority after resume

The standalone scenario extends [E2E-002](e2e-002-continuity.md) with real local
grant validation, agentd bootstrap issuance and PostgreSQL credential fencing.
It changes only tests and documentation; no production contracts or schemas change.

## Assertions

- Both HTTP-admitted Executions have distinct grants and generations. The second
  admission's idempotent replay returns the same Execution without extra grants.
- Resume/readiness and resume replay create no Execution, grant or credential.
- Production `agentdidentity` and `localissuer` issue fresh P-256 leaf keys and
  random bootstrap proofs under one ephemeral client-only CA. The actual key pairs,
  certificate chains, tenant/Sandbox/Attempt SANs and grant-bounded expiry verify.
- The first proof is consumed and its certificate successfully reconnects before
  retirement. After compute replacement, while the old grant and certificate are
  still within their original validity intervals, the grant remains `CANCELLED`;
  changing its generation/version fails validation. New credential issuance for
  the old identity, old bootstrap consumption, reconnect and connection checks fail.
- A newly constructed registry and issuance service use persisted state. The new
  identity rejects the old proof, accepts its own proof once, rejects replay and
  passes the current connection check. Both grants are cancelled at completion.
- Exported Workspace, rollout, restore metadata and signed manifest exclude the
  actual private-key PEM and raw/base64/hex proof values. Existing guest canary,
  fresh-home/environment and restored-conversation assertions remain in force.
- Session, Workspace and conversation continuity still pass through two real Kata
  Sandboxes, with the original Sandbox, Pod and PVCs absent before reconstruction.

## Scope

Grant snapshots are **not bearer credentials**. The test separately exercises
actual transport keys/proofs rather than treating a changed grant ID as proof of
fresh secrets. The credential-authority composition is a test fixture: it reloads
`LoadLocalBinding`, calls production LocalAuthority validation, checks the current
compute binding, and delegates atomic registration/fencing to PostgreSQL.

Credential issuance and registry calls run in the controller. Secrets stay in
controller memory, with delivery buffers cleared after the test; no credential is projected into
the guest or used for a TLS handshake. The actual guest remains on the existing
operator Kubernetes-exec transport with a deterministic loopback model. The export
scan is a regression check, not evidence of a hostile guest handling these secrets.
Live authenticated transport, production provider admission, gateway credential
issuance/injection/revocation, concurrent grant cancellation during issuance and
executable lifecycle workers remain outside this scenario. Existing E2E-001 fixture
and local-path storage limitations apply.

## Reproduce

Use the [standalone runner](../../test/e2e/standalone/README.md) and require all
three `E2E-001/002/003 LIVE PASS` markers plus Go PASS. The guest code is unchanged;
this run uses the already imported E2E-002 probe image:

`docker.io/library/thinkpixel-codex@sha256:a66d1a419f7bf0e68bea58922b33f2f759c93dcbde4dc1c8daea147e1a754a95`

## Observed run — 2026-09-27

Passed in **110.51 seconds** against disposable PostgreSQL database
`thinkpixelar_e2e003` with the current 33 migrations, using `k3spi-02` and
`kata-qemu-runtime-rs-ar331`. Both Pods passed trusted-worker QEMU/KVM and pin checks.
All three LIVE PASS markers and Go PASS were observed.

| Record | Value |
| --- | --- |
| Namespace | `ar-e2e001-20260927162002-1279615` |
| Session | `01a0e3aa-a022-733d-a5c9-4e8a336bf492` |
| Checkpoint | `01a0e3ab-6acf-7788-9e2f-6cc561889a8f` |
| First Execution | `01a0e3aa-a068-7bf9-906c-ac100f2ba730` |
| Second Execution | `01a0e3ac-1b44-73b2-aa98-08c56780d31c` |
| First local grant | `01a0e3aa-a06c-75df-82b6-176ae9ea213a` |
| Second local grant | `01a0e3ac-1b47-7110-b9ab-34bda77098f9` |
| First credential ID | `01a0e3ab-323a-785b-92b6-79581276aa29` |
| Second credential ID | `01a0e3ac-1bd4-7397-8215-e31bfb60f25e` |
| Old Pod UID | `6d517f66-f538-45a8-9af7-6982cc836c87` |
| Replacement Pod UID | `c6172253-8536-4021-bf69-1f378629cf18` |

Additional checks passed: `go vet ./internal/adapters/http` and race-enabled tests
for `internal/adapters/authority/local`, `internal/app/agentdidentity` and
`internal/adapters/sandboxtransport/localissuer`. Ordinary standalone tests compile
and skip without opt-in; the live run above is the scenario evidence.

The printed test namespace and disposable database were deleted after verification.
