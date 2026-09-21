# Phase 3 — Kubernetes Agent Sandbox substrate

Completed 2026-09-21 for the existing ARM64 homelab scope selected by ADR-0014.
KAS-001 through KAS-024, including KAS-008A and KAS-013A, are implemented and
verified. This is substrate completion; an end-user release still requires the
supervisor, harness and standalone execution composition in Phases 4–6.

## Implementation lineage

Each implementation commit includes its plan/checklist updates. Accepted decisions
are indexed in [ADRs 0005–0021](adr/README.md); the
[version matrix](supported-versions.md) records the exact supported/tested scope.

| Items | Commits | Result |
| --- | --- | --- |
| KAS-001–003 | `d6442fc`, `26cf754`, `696a3c5` | Trusted HTTPS client, pinned beta API, immutable Runtime Profiles. |
| KAS-004–008 | `2820dbe`, `86454b4`, `b00afb7`, `afda458`, `8c86980` | Reserved acquisition, conservative state, exact-UID release and native suspend/resume. |
| KAS-008A | `0dff888` | [Homelab Kata installation](evidence/kata-homelab-installation.md). |
| KAS-009–013A | `9934897`, `104dcf2`, `8227c9d`, `dd0b35a`, `b6cc6af`, `5adc795` | Immutable templates, durable operation journal, Workspace seam, runtime mapping, effective guards and explicit homelab profiles. |
| KAS-014–015 | `ac13a0a`, `7d95985` | Network enforcement hooks and [live scoped denial](evidence/kas-015-network-denial.md). |
| KAS-016–018 | `bfedb75`, `144466d`, `e81fa99` | Reconciliation, restart-safe monitoring and fenced compute-loss recovery. |
| KAS-019–020 | `64aa323`, `3da0f22` | [Cold lifecycle](evidence/kas-019-live-lifecycle.md) and [native suspend/resume](evidence/kas-020-live-suspend-resume.md). |
| KAS-021 | `2c6da07` | [Host-correlated hardware KVM isolation](evidence/kas-021-live-isolation.md). |
| KAS-022 | `603fc10`, `05cf542` | Process ceiling, then [physical scratch/compute enforcement](evidence/kas-022-bounded-resources.md), superseding the recorded scratch failure. |
| KAS-023 | `848e4b3` | [Upstream compatibility policy](evidence/kas-023-upstream-compatibility.md). |
| KAS-024 | This closure commit | Required capability discovery, reviewed schema pin, final live regression and this evidence. |

## Final validation

- Focused race-enabled adapter tests reject missing discovery, unsupported server,
  missing verbs, wrong scope, schema/storage-version drift, incomplete controller
  rollout, image mismatch, oversized discovery, cancellation and API outage.
  Acquisition fails before reservation; a stale immutable template is rejected;
  discovery failure withholds secure READY.
- Real PostgreSQL migration/store and reconciliation tests passed with `-race`
  against the existing isolated development test database, without resetting it.
- `make verify` passed: formatting, static checks, unit/race tests, generated/API
  checks, dependency/license/hygiene/version checks, vulnerability scan and builds.
- Race-enabled live cold lifecycle, native suspend/resume and read-only discovery
  all passed on worker `k3spi-02`, using the bounded Kata RuntimeClass and fresh
  32 MiB local scratch filesystems. Cold lifecycle took 23.36 s; native lifecycle
  36.99 s; final discovery 0.17 s. Lifecycle assertions cover owner identity,
  absolute deadline, Pod replacement, scratch identity replacement, scratch-PVC
  removal and retained durable Workspace PVCs.

The final retained namespaces are
`ar-live-01a0c4ed-1ead-7378-8d1a-1eea645f175e` and
`ar-live-01a0c4ed-79ee-7623-9303-7d8135e7b47d`.
Native resume changed Pod UID from `f17405b6-5e0e-4cb0-b94c-c66416b29fc4` to
`649f8dd3-e71b-4a27-8094-4375ab5511e0`, preserving Sandbox identity/deadline.
It replaced scratch PVC UID `ff842351-5bcd-4123-baeb-318635b8e687` with
`426a4a43-840b-418a-a768-2e875b7d64fb`, using different retained PVs.

The reviewed core CRD spec pin is
`sha256:8585683179711c07eb8c8406859f39430b23c91b3959a9350a17d7f44082f427`.
The final observed capability digest is
`sha256:f62eca00270f9c9c6203099d86b11aaf08ffd1e6452c4119722c5302f551efba`.
It binds this installation's CRD/controller identities and generation; another
installation must resolve its own digest. Pin generation was exercised against
the retained reviewed manifest using server dry-run, without changing the CRD.

Reproduce using the [Agent Sandbox runbook](operations/agent-sandbox.md),
[Kata runbook](operations/kata.md) and [scratch runbook](operations/bounded-scratch.md).
The checked-in overlay, runtime mapping, profiles, storage class and pin are
reviewable operator inputs. Credentials and sensitive runtime payloads are absent.

## Scope and remaining release work

- Live fixtures deliberately withhold secure AR READY without an effective
  verifier. Phase 6 must connect infrastructure proof, Workspace allocation,
  bootstrap and fresh authority to admission. Separate passing substrate tests
  are not a substitute for that composition.
- Scratch uses a finite operator-prepared pool, retained after release, with no
  automatic recycling. Root filesystem is read-only. An aggregate hard quota
  over host logs/image layers is not claimed. The local-path durable Workspace
  fixtures do not prove capacity enforcement, encryption, snapshots or node-loss
  durability; real WorkspaceProvider composition remains Phase 6 work.
- Runtime mount/agent dependency configuration was checked; an actual node reboot
  was not exercised. Production density, node disruption, amd64, encrypted CSI
  snapshot/restore, IPv6 and complete gateway endpoint enforcement were not
  qualified by these ARM64 tests. Warm pools/claims remain unselected by ADR-0010.
  A pre-v1 upgrade path was not exercised.
- [Future infrastructure requirements](operations/rc-infrastructure.md) specify
  Linux amd64/KVM workers (starting at 8 cores/32 GiB, at least two workers),
  suitable SSD headroom and encrypted, snapshot-capable single-pod-writer storage
  for the production profile. These future tests do not require buying hardware
  to continue the homelab release candidate.

No paid infrastructure was added. Existing default runc and unrelated workloads
remain outside the selected bounded Kata lane.
