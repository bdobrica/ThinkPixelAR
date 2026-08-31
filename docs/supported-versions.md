# Supported component versions

Status: Normative Phase 0 compatibility baseline.

Last upstream review: 2026-08-28.

## Development toolchain

| Component | Exact version | Status | Verification |
| --- | --- | --- | --- |
| Go | `go1.26.7` | `TESTED` | Official `linux/amd64` archive verified against Go release metadata, then used to run `go version`, `go mod tidy`, and `go list -m -json` on 2026-08-31. Package test and vet gates begin after ENG-002 adds packages. |
| PostgreSQL development service | `18.6-alpine3.24`; OCI index digest `sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2` | `PINNED_DEVELOPMENT` | Docker Official Image pinned for repeatable local development. This is not production qualification; integration behavior begins with DB-001. |
| Go container build stage | `1.26.7-alpine3.23`; OCI index digest `sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57` | `PINNED_BUILD` | Docker Official Image used only to compile the statically linked service binary. |
| Service runtime base | Distroless `static-debian13:nonroot`; OCI index digest `sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7` | `PINNED_RUNTIME` | Minimal shell-less runtime with numeric non-root identity; smoke-tested separately from Kubernetes qualification. |
| `thinkpixel-agentd` runtime base | Distroless `static-debian13:nonroot`; OCI index digest `sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7` | `PINNED_RUNTIME` | Same reviewed minimal runtime, packaged as a separate supervisor-only image with no vendor harness. |

The module path is `github.com/bdobrica/ThinkPixelAR`. The `go` directive in
`go.mod` and `.go-version` both pin Go `1.26.7`; development and CI environments
MUST use that exact patch release until the pin is deliberately updated and the
verification commands are rerun. Go `1.26` remains supported under the upstream
policy until Go `1.28` is released.

## Meaning of support

ThinkPixelAR uses exact qualification pins. A component is **release-qualified** only after the required clean-cluster, lifecycle, recovery, storage, and isolation evidence passes with the exact patch versions and immutable artifacts recorded here. Source-level compatibility or an upstream support statement alone is insufficient.

The Phase 0 values below are the **initial qualification baseline**. They freeze implementation targets and API contracts; they are not yet a claim that the empty repository has run the later Kubernetes/Kata end-to-end gates. Each row remains `PINNED_CANDIDATE` until its evidence link/digest is added by the implementation phase. Production release artifacts MUST NOT describe a candidate as tested.

Patch upgrades require the compatibility workflow below. Minor upgrades require an explicit review and a dedicated qualification lane before changing the default. Mutable tags such as `latest`, unpinned install URLs, or distribution-default versions are not support evidence.

## Initial qualification baseline

| Component | Exact version/API pin | Status | Rationale and required evidence |
| --- | --- | --- | --- |
| Kubernetes | `v1.36.2` | `PINNED_CANDIDATE` | Supported Kubernetes branch and the dependency generation explicitly targeted by Agent Sandbox `v0.5.x`. Run conformance/integration on the exact server and kubelet patch; record node/control-plane images and digests. |
| Kubernetes Agent Sandbox | `v0.5.5`; core `agents.x-k8s.io/v1beta1`; extensions `extensions.agents.x-k8s.io/v1beta1` | `SUPERSEDED_CANDIDATE` | Previously reviewed candidate, now behind upstream `v1.0.0`. `PH0-KAS-001` requires selecting/testing the current line; do not claim production support. |
| Kata Containers | `3.31.0`; containerd shim v2; `runtime-rs` default; QEMU initial VMM | `PINNED_CANDIDATE` | Current reviewed immutable release. Qualify `amd64` first and `arm64` before advertising it; record kernel/rootfs/hypervisor/agent/runtime artifact digests and effective RuntimeClass handler. |
| Container runtime | containerd `2.3.1`; CRI `runtime.v1`; config schema accepted by the selected distribution; shim v2 | `PINNED_CANDIDATE` | Current reviewed LTS line/patch and Kata integration substrate. Qualify exact distribution build/config and runtime handler; upstream semantic version alone is insufficient. |
| CSI snapshot controller/client | external-snapshotter `v8.6.0`; `snapshot.storage.k8s.io/v1` | `PINNED_CANDIDATE` | Current reviewed GA snapshot API/controller line. Install CRDs/controller by immutable image digest and qualify against the selected CSI driver. VolumeGroupSnapshot is not required. |
| CSI attach sidecar (when required by driver) | external-attacher `v4.12.0`; CSI spec minimum `1.0.0` | `PINNED_CANDIDATE` | Current reviewed GA release with Kubernetes 1.36 recommended upstream. Driver packaging may own this sidecar; evidence records its effective version/digest rather than duplicating it. |
| CSI storage driver | Operator-selected, exact vendor/version pin required per deployment profile | `UNSELECTED_BLOCKING_QUALIFICATION` | AR remains vendor-neutral, but standalone MVP evidence must select one real CSI driver/version and record provisioner, node plugin, sidecars, StorageClass, VolumeSnapshotClass, topology, and capabilities. No generic compatibility claim substitutes for this. |

### Why Kubernetes 1.37 is not the initial pin

Kubernetes `1.37.0` was released on 2026-08-26. At this review, Agent Sandbox's documented dependency upgrade explicitly supports Kubernetes 1.36, while no release evidence reviewed here qualifies `v0.5.5` plus Kata/storage on Kubernetes 1.37. ThinkPixelAR therefore pins `1.36.2` for its initial test lane and lists `1.37.0` as `EVALUATE_AFTER_BASELINE`, not supported. This is intentional conservatism, not a claim that 1.37 is incompatible.

## Kubernetes Agent Sandbox feature matrix

Review update 2026-08-28: official tags now include `v0.5.6` and `v1.0.0`. The v1 source retains `v1beta1`, removes `v1alpha1`, and changes/clarifies Service, shutdown and managed NetworkPolicy behavior. See the [Phase 0 cross-system review](evidence/phase-0-cross-system-review.md). The table below preserves prior v0.5.5 analysis as historical input, not current support evidence.

| Feature | Upstream surface | RC disposition | Qualification requirement |
| --- | --- | --- | --- |
| Core Sandbox | `agents.x-k8s.io/v1beta1` `Sandbox` | Required | Create, readiness translation, effective-spec verification, loss/recreate, release, restart/idempotency. |
| SandboxTemplate | `extensions.agents.x-k8s.io/v1beta1` `SandboxTemplate` | Required for reusable profile mapping | Immutable digest/profile mapping, forbidden-field rejection, update behavior. |
| SandboxClaim | `extensions.agents.x-k8s.io/v1beta1` `SandboxClaim` | Required where used by initial acquisition path | Targeted cold claim, adoption identity, duplicate/restart safety, release. Do not rely on deprecated `v1alpha1` fields. |
| Suspend/resume | `Sandbox` lifecycle/conditions in `v0.5.5` | Required if used; otherwise release+replacement implements logical suspension | Inspect the boolean `Suspended` condition; do not infer suspension from condition presence. Verify volume/process/network semantics and controller restart. |
| SandboxWarmPool | `extensions.agents.x-k8s.io/v1beta1` `SandboxWarmPool` | Deferred optimization | Not on correctness critical path. Before enablement, prove no tenant/session/process/environment/credential/vendor-state residue and safe fallback to cold acquisition. |
| Router/SDK data-plane operations | Optional upstream router/clients | Not selected by ARC-015 | ARC-019 selects authenticated AR↔`agentd` transport. No unauthenticated router is accepted implicitly. |
| Snapshot extensions/examples | Provider/cloud specific | Not canonical AR checkpoint API | May inform adapters; WorkspaceProvider/Checkpoint contracts remain AR-owned and vendor-neutral. |

Agent Sandbox release `v0.5.4` changed `Suspended` condition semantics: the condition is consistently present and clients inspect its `status`. The initial adapter targets the corrected behavior inherited by `v0.5.5`. Releases `v0.5.0` and `v0.5.1` are explicitly unsupported because upstream documented a warm-claim reconciliation race; upgrades from `v0.4.x` follow upstream's `v0.5.2+` migration procedure before reaching the pin.

## Kata and containerd matrix

| Capability | Initial requirement |
| --- | --- |
| CRI | Kubernetes and kubelet use CRI `runtime.v1`; deprecated CRI v1alpha2 is not a target. |
| Runtime integration | containerd shim v2, exact Kata runtime handler configured by the operator and selected only through abstract `microvm-strong`. |
| VMM | QEMU initial qualification. Cloud Hypervisor/Firecracker variants require separate Runtime Profile and evidence; they are not aliases. |
| Architecture | `amd64` initial required lane. `arm64` is advertised only after separate hardware/virtualization and full e2e evidence. |
| Host virtualization | Hardware virtualization and `/dev/kvm` (or provider-supported equivalent) available to trusted Kata runtime components, never directly to the sandbox workload. |
| Guest artifacts | Kernel, root filesystem/image, `kata-agent`, runtime/shim, hypervisor, firmware/config are pinned by digest/package provenance in evidence. |
| Isolation proof | Effective Sandbox Pod uses intended RuntimeClass/handler; process/namespace/mount/device/network probes demonstrate guest boundary; `kata-runtime check`/runtime diagnostics pass on every eligible node. |
| Security defaults | No service-account token, host namespace, hostPath, privileged workload, runtime socket, excess capability, metadata/API egress, or unbounded resources. Kata does not replace these controls. |
| Upgrade | Drain/replace nodes; do not mutate runtime artifacts beneath a live Sandbox. Existing Session continuity resumes from durable state after qualified replacement. |

ThinkPixelAR does not expose the concrete handler name (for example `kata-qemu`) in its public API. Operator configuration maps `microvm-strong` to the exact installed handler and AR records the effective mapping digest.

## CSI feature matrix

| Storage capability | Kubernetes/API level | RC requirement | Driver qualification |
| --- | --- | --- | --- |
| Dynamic PVC provisioning | core `v1` PVC/StorageClass, CSI driver | Required | Create/bind/mount/restart/delete; exact capacity and topology; idempotent cleanup. |
| Session Workspace mount | filesystem volume, `ReadWriteOnce` minimum | Required | Survives Sandbox/Pod/node replacement as promised by selected backend; no cross-Session attach; mount at `/workspace`. |
| Vendor state mount | same Workspace volume or separately governed durable volume | Required | `/state/<vendor>` boundary, permissions, checkpoint inclusion, credential exclusion. |
| VolumeSnapshot | `snapshot.storage.k8s.io/v1`, external-snapshotter `v8.6.0` | Required for the initial snapshot-backed checkpoint provider | Crash consistency/application quiesce, readiness, restore, deletion policy, integrity/provenance, controller restart. |
| Restore PVC from snapshot | core PVC `dataSource` → `VolumeSnapshot` | Required with snapshot provider | Same-tenant/namespace binding, requested capacity, topology, independent lifecycle, failed/partial restore cleanup. |
| PVC clone | core PVC `dataSource` → PVC; CSI driver-specific | Capability-gated, not assumed | Source bound/available, same namespace, compatible VolumeMode/capacity/storage backend, independent writes/deletion. ARC-024 decides RC fork requirement. |
| Volume expansion | CSI driver/filesystem-specific | Optional; never implicit | Only explicit operator operation; no silent resource-envelope widening. |
| `ReadWriteOncePod` | driver/sidecar/Kubernetes support | Preferred defense in depth | Verify scheduling and attach behavior. AR single-writer fencing remains authoritative even if unavailable. |
| Multi-writer (`ReadWriteMany`) | driver-specific | Not required | Does not permit concurrent mutable Executions; requires separate security/correctness review. |
| Group snapshots | `groupsnapshot.storage.k8s.io/v1` in external-snapshotter `v8.6.0` | Not required | Future capability only; no dependency in checkpoint semantics. |
| Cross-namespace data source | feature/policy dependent | Forbidden initially | Source and destination remain in the Session's controlled namespace boundary. |
| Generic/CSI ephemeral volumes | Kubernetes stable but driver-specific | Not a durable Workspace | May serve bounded scratch storage only and is excluded from Session durability claims. |

VolumeSnapshot GA API support does not mean a selected CSI driver implements safe snapshot/restore for ThinkPixelAR. Driver/controller versions, sidecars, backend configuration, filesystem quiescing, topology, encryption, deletion policy, and restore behavior are a single qualified tuple.

## Compatibility policy

### Exact pins and skew

- Control-plane/server, kubelet, Agent Sandbox controller/CRDs, containerd, Kata artifacts, CSI driver/sidecars, and snapshot CRDs/controllers are recorded by exact version plus immutable image/package digest in evidence.
- ThinkPixelAR supports only tuples present in this document and their linked evidence. Arbitrary mixes inside semver ranges are unqualified.
- `kubectl` skew follows Kubernetes policy for operators, but AR's generated client dependency and server discovery are tested against the exact server pin.
- Agent Sandbox API discovery must serve `v1beta1`; deprecated `v1alpha1` is not used by AR.
- Unknown fields/states/conditions and missing required capabilities fail closed. AR does not parse version strings to guess features.

### Patch update procedure

1. Review upstream release notes, security advisories, API/CRD/schema diffs, images, SBOM/provenance, and transitive compatibility.
2. Pin artifacts/checksums/digests in a proposed change; never update a floating manifest.
3. Run clean install and upgrade/rollback tests where supported.
4. Run PostgreSQL-independent provider conformance, Kubernetes lifecycle/restart/loss/reconcile, storage snapshot/restore, effective-profile security, and Kata adversarial e2e lanes.
5. Record environment, exact artifacts, commands, results, known deviations, performance baseline, and commit.
6. Change the status to `RELEASE_QUALIFIED` only after required gates pass.

A critical security fix may remove support immediately. It never authorizes an untested automatic upgrade underneath live Sessions.

### Minor update procedure

Minor changes require all patch steps plus explicit API conversion/migration review, fresh cluster qualification, upgrade from the previous supported tuple, stored-object migration/rollback plan, performance/capacity comparison, and an ADR if public/domain assumptions change.

## Required qualification environments

1. **Disposable functional cluster:** Kubernetes `v1.36.2`, Agent Sandbox `v0.5.5`, selected CNI and CSI tuple; validates cold lifecycle, claims/templates, Workspace, snapshot/restore, controller/AR restart, and node loss.
2. **Kata-capable security cluster:** same versions plus containerd `2.3.1` and Kata `3.31.0` QEMU on hardware virtualization; validates effective runtime, hostile probes, network/credential boundaries, complete compute replacement, and resume.
3. **Upgrade cluster:** previous supported tuple to proposed tuple with active/idled/suspended Sessions and cleanup/orphan checks.

Kind without Kata may run fast functional/provider tests but cannot qualify `microvm-strong` or the secure coding profile.

## Upstream sources reviewed

- Go release history and support policy: <https://go.dev/doc/devel/release>
- Go `1.26.7` archive metadata: <https://go.dev/dl/?mode=json&include=all>
- PostgreSQL 18.6 release and Docker Official Image: <https://www.postgresql.org/about/news/postgresql-186-1711-1615-1519-1424-and-19-beta-3-released-3365/> and <https://hub.docker.com/_/postgres>
- Kubernetes releases and patch support: <https://kubernetes.io/releases/> and <https://kubernetes.io/releases/1.37/>
- Kubernetes storage snapshot/clone concepts: <https://kubernetes.io/docs/concepts/storage/volume-snapshots/> and <https://kubernetes.io/docs/concepts/storage/volume-pvc-datasource/>
- Kubernetes Agent Sandbox releases (`v0.5.5`) and API migration: <https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v0.5.5> and <https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/api-migration-guide.md>
- Kata Containers releases and installation: <https://github.com/kata-containers/kata-containers/releases/tag/3.31.0> and <https://github.com/kata-containers/kata-containers/blob/main/docs/installation.md>
- containerd releases/support policy: <https://github.com/containerd/containerd/releases/tag/v2.3.1> and <https://github.com/containerd/containerd/blob/main/RELEASES.md>
- CSI snapshotter `v8.6.0`: <https://github.com/kubernetes-csi/external-snapshotter/releases/tag/v8.6.0>
- CSI attacher `v4.12.0`: <https://github.com/kubernetes-csi/external-attacher/releases/tag/v4.12.0>
