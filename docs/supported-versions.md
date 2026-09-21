# Supported component versions

Status: Normative tested and candidate compatibility baseline.

Last repository and homelab scope review: 2026-09-20.

## Development toolchain

| Component | Exact version or artifact | Status | Reproducible verification |
| --- | --- | --- | --- |
| Go | `go1.26.7` | `TESTED_REPOSITORY` | `make versions-check` verifies the running toolchain against `.go-version` and `go.mod`; `make verify` compiles, analyzes, vulnerability-scans, and tests all packages. |
| Node.js / npm | Node.js `24.11.1`; npm `11.6.2` | `TESTED_REPOSITORY` | `make versions-check` verifies both executables against `package.json`; CI provisions the exact Node.js patch and `npm ci` enforces the lock file. |
| OpenAPI CLI | Redocly CLI `2.49.0` | `TESTED_REPOSITORY` | Exact direct dependency in `package.json` and `package-lock.json`; `make openapi-check` validates and regenerates to a temporary file to detect drift. |
| Static analysis | Staticcheck `v0.8.1` | `TESTED_REPOSITORY` | Exact `go run` tool pin in `Makefile`; executed by `make verify`. |
| Vulnerability scanner | govulncheck `v1.7.0` | `TESTED_REPOSITORY` | Exact `go run` tool pin in `Makefile`; executed by `make verify`. |
| PostgreSQL development service | `postgres:18.6-alpine3.24@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2` | `TESTED_DEVELOPMENT` | Immutable Compose pin; real health/version smoke evidence is recorded in [ENG-012](evidence/eng-012-postgresql.md). This is not production qualification; integration behavior begins with DB-001. |
| Go container build stage | `golang:1.26.7-alpine3.23@sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57` | `TESTED_BUILD` | Both Dockerfiles use this immutable build-only artifact; real builds are recorded in [ENG-013](evidence/eng-013-container-image.md) and [ENG-014](evidence/eng-014-agentd-image.md). |
| Service and `thinkpixel-agentd` runtime base | `gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7` | `TESTED_RUNTIME_BASE` | Both final images use this immutable artifact. Their separate non-root, read-only, capability-drop, and no-new-privileges smoke evidence is recorded in ENG-013 and ENG-014. This is not Kubernetes qualification. |

`TESTED_REPOSITORY` means the exact tool version has run the repository gate.
`TESTED_DEVELOPMENT`, `TESTED_BUILD`, and `TESTED_RUNTIME_BASE` are narrower
claims described in their evidence; none imply that the later cluster tuple is
release-qualified. `make versions-check` fails when the running Go/Node/npm
toolchain differs from its source pin, when Docker/Compose pins drift, or when
this table stops recording those exact artifacts.

The module path is `github.com/bdobrica/ThinkPixelAR`. The `go` directive in
`go.mod` and `.go-version` both pin Go `1.26.7`; development and CI environments
MUST use that exact patch release until the pin is deliberately updated and the
verification commands are rerun. Go `1.26` remains supported under the upstream
policy until Go `1.28` is released.

## Meaning of support

Compatibility claims apply to an exact tuple and a named test scope.
`TESTED_HOMELAB_LIFECYCLE` covers the recorded controller operations;
`TESTED_HOMELAB_ISOLATION` covers hardware-backed guest execution. Neither means
`RELEASE_QUALIFIED`, complete secure-profile admission, or production support.
The [fixed KAS-022 lane](evidence/kas-022-bounded-resources.md) now proves bounded
scratch and compute resources on the homelab. Earlier emptyDir failures remain
unqualified; full end-user admission still needs complete composed proof.

The initial Phase 0 candidates are historical input. ADR-0006 selects Agent
Sandbox v1.0.0 and Kubernetes client modules v0.36.4; ADR-0014 selects existing
ARM64 homelab hardware for RC work. Larger amd64/encrypted/snapshot-capable
infrastructure is future production qualification, not an RC purchase requirement.

## Current substrate matrix

| Component | Exact implementation / live tuple | Status and evidence |
| --- | --- | --- |
| Kubernetes client / server | Go modules `v0.36.4`; homelab K3s server/kubelets `v1.36.4+k3s1` | `TESTED_HOMELAB_LIFECYCLE`; [KAS-019](evidence/kas-019-live-lifecycle.md), [KAS-020](evidence/kas-020-live-suspend-resume.md). This does not qualify every upstream/distribution build of Kubernetes 1.36.4. |
| Agent Sandbox | `v1.0.0`, source `bb72f49d79f009a960eed2ae6c32e1cc082399c5`; core `agents.x-k8s.io/v1beta1` | Core CRD/controller clean-installed and live-tested. Manifest checksum and controller index/platform digests are pinned in [installation assets](../deploy/agent-sandbox/) and [evidence](evidence/kas-019-live-lifecycle.md). |
| Agent Sandbox extensions | `extensions.agents.x-k8s.io/v1beta1` API types from `v1.0.0` | `TESTED_SOURCE_ONLY`; immutable template mapping and API registration. Extension controller/CRDs are not installed or required by the selected direct cold path. |
| Kata | `3.31.0`, QEMU, runtime-rs, reviewed ARM64 overrides | `TESTED_HOMELAB_ISOLATION`; [exact host-correlated artifact hashes](evidence/kas-021-live-isolation.md). The bounded runtime-rs handler and physically bounded scratch now pass the scoped [resource lane](evidence/kas-022-bounded-resources.md); the Go comparison remains unqualified. |
| Container runtime | containerd `2.3.4-k3s1.36`, CRI `runtime.v1`, config schema 3, shim v2 | Live tuple from K3s, superseding the historical upstream 2.3.1 candidate for this lane. Not a claim for arbitrary containerd builds. |
| Host | ARM64 Raspberry Pi workers; Linux `6.18.39+rpt-rpi-v8`, hardware KVM | Existing three-worker homelab; installation probes on all workers, detailed KAS-021/022 canary on worker02. The bounded worker02 lane uses the measured conservative overhead from KAS-022; broader density remains unqualified. |
| CNI / policy | K3s-packaged networking and network-policy controller from `v1.36.4+k3s1` | [Scoped IPv4 TCP denial evidence](evidence/kas-015-network-denial.md). This does not qualify IPv6, DNS identity or arbitrary network classes. |
| Local Workspace fixture | K3s `local-path`, node-local RWO PVCs | Lifecycle attachment only; unencrypted, no snapshot/fork or cross-node durability. PVC requested size is not a hard quota. Full WorkspaceProvider/bounded-storage work remains. |
| amd64 PC profile | `coding-homelab-amd64` | Schema/template support; no live amd64 evidence yet. Requires compatible Linux/KVM and a separately tested tuple. |
| Larger secure profile | `coding-medium-secure` | Future deployment target. [Required infrastructure and tests](operations/rc-infrastructure.md); not an RC infrastructure prerequisite. |

The source/deployment pin remains Kubernetes 1.36.4. Kubernetes 1.37 and other
patches/distributions need a separate compatibility run; no compatibility or
incompatibility is inferred from a release number alone. No complete tuple is
currently release-qualified.

## Agent Sandbox API assumptions

The following reflects the implemented v1.0.0 adapter and accepted ADRs, not a
claim to support every upstream API feature.

| Surface | AR behavior and compatibility obligation |
| --- | --- |
| Core Sandbox | Direct cold create/get/update/delete in the configured namespace. Stable AR reservation precedes create; replay retains the exact native UID. Bound-but-missing compute becomes recovery work rather than silent recreation. |
| Backing Pod | Same name as Sandbox; verify controller owner reference and Sandbox UID. Do not depend on the removed `agents.x-k8s.io/pod-name` annotation. |
| Conditions | Inspect status and matching `observedGeneration`. A present but false `Suspended` condition is not suspension. Unknown or unavailable physical state is not absence. Native Ready alone is not secure AR READY. |
| Suspend/resume | `spec.operatingMode` is desired state. Confirm true suspension and actual Pod absence; resume preserves Sandbox UID and absolute shutdown deadline, with a new Pod. This does not implement Session checkpoint/restore or preserve authority. |
| Deadline / release | Set absolute shutdown time. Delete only the owned UID with resource-version precondition and foreground propagation; confirm absence separately. Workspace PVCs remain outside provider deletion ownership. |
| Service | Explicitly disabled for outbound agentd transport (ADR-0002). No router, upstream sandboxd or SDK execution endpoint is selected. |
| SandboxTemplate | Immutable adapter-local typed mapping; direct acquisition uses its blueprint. Source support does not require installing the extension controller. |
| SandboxClaim / warm pool | Not used. ADR-0010 selects direct cold acquisition because claims require a warm-pool reference. No claim adoption, pool residue or warm capacity is qualified. |
| Network | AR preinstalls/verifies an explicit dedicated-namespace policy. Upstream managed-policy defaults are not implicit permission for Internet access. |
| Workspace / bootstrap | Caller-resolved PVC attachments and a dedicated read-only bootstrap reference; no provider-created Workspace or copied Kubernetes credential. Live tests use an empty fixture Secret. |
| Legacy alpha API | Unsupported. Only beta types are registered; no conversion webhook, alpha fallback or in-place migration is implemented by AR. |

The provider-neutral [SandboxProvider contract](contracts/sandbox-provider.md)
remains authoritative. [ADR-0021](adr/0021-sandbox-capability-discovery.md) implements
bounded live discovery before acquisition, native mode changes and secure READY.
The trusted schema/controller capability digest is bound into the immutable
template configuration. Production service composition remains Phase 6 work;
live fixtures intentionally withhold secure readiness without an effective verifier.

## Storage capability policy

Storage requirements come from the immutable profile. The homelab profiles select
single-writer local storage, no encryption requirement and disabled snapshots.
The original larger profile still requires its stronger backend. A disabled
capability must fail explicitly rather than silently select weaker storage.

| Capability | Qualification required before use |
| --- | --- |
| Workspace / vendor-state PVC | Exact namespace/name/UID, topology, actual bounded capacity, ownership/fencing, mount identity and promised restart durability. RWO alone is not AR's single-writer fence. |
| Snapshot / restore | Selected driver and backend, `snapshot.storage.k8s.io/v1`, quiescing, integrity, same-tenant restore, deletion policy, outage and restart behavior. Not required by snapshot-disabled homelab profiles. |
| RWOP / clone / fork | Explicit driver/sidecar support and adversarial concurrency/independent-write tests. Never inferred from a generic API version. |
| Expansion / cross-node restore | Explicit profile/operator action and separately qualified backend; no silent resource widening or local-volume roaming claim. |

Historical future-backend candidates remain external-snapshotter `v8.6.0` and
external-attacher `v4.12.0` where required by the selected driver. They are not
installed, tested or required by the current homelab lane. Select exact driver,
sidecar, backend and immutable artifact pins together before enabling them.

## Upstream compatibility and upgrade policy

Exact Go module pins, CRDs, release manifest checksum, controller image index and
platform manifests, Kata installer/guest/VMM artifacts, runtime configuration,
K3s packaging, CNI and storage behavior form one evidence tuple. A semver range,
RuntimeClass name or successful Pod startup does not qualify a substitution.

For every proposed patch or minor update:

1. Review upstream release/security notes and diff API types, CRD schemas/defaults,
   RBAC, conditions, naming, shutdown, network policy and stored-version behavior.
2. Pin new source/artifact digests and render from checked-in source; review the
   output. Never apply an unpinned remote manifest or hand-edit generated artifacts.
3. Run adapter API/translation/security/replay tests, real PostgreSQL reconciliation
   tests, then `make verify`.
4. On the same free homelab or another explicitly selected test lane, run clean
   install, cold lifecycle, native suspend/resume if used, network denial, host KVM
   correlation and physical resource/storage checks. Requalify changed configuration
   even when its release version stays the same.
5. Before supporting an upgrade path, test it separately with retained objects,
   controller/AR restarts, ambiguous responses and exact cleanup ownership. Prepare
   an actual data/CRD migration and rollback procedure; do not infer downgrade safety.
6. Record the exact tested scope, failures and evidence. Preserve old Execution
   resolution snapshots; new configuration must not mutate their authority or limits.
   Promote only passing capabilities. Core isolation failures remain blockers.

A minor/major change also needs explicit API conversion and stored-object review,
performance/capacity comparison, and an ADR if it changes an accepted assumption.
Security fixes can remove support but cannot authorize an untested automatic
upgrade under active workloads. Pause scheduling and replace compute safely before
changing runtime artifacts; Session continuity is a separate AR responsibility.

### Existing pre-v1 Agent Sandbox clusters

The homelab was a fresh core-controller install, **not an upgrade rehearsal**.
The upstream v1 release removes alpha APIs and conversion webhooks. Existing
pre-v0.5 installations must first reach the documented v0.5.2+ migration path;
rewrite stored resources to beta and verify `status.storedVersions` before v1.
Inspect every installed core/extension CRD, including retained resources. Follow
the upstream migration and cleanup instructions with backups and a tested rollback
plan; the AR renderer is not a migration tool. See the
[v1.0.0 release and migration requirements](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0).

The repository does not claim tested v0.5→v1 upgrades or compatibility with an
existing deployment merely because it serves beta endpoints. Optional extension
components need their own install/conformance evidence before AR starts using them.

## Required lanes and current limits

- Existing ARM64 homelab: current low-cost lifecycle/isolation/resource lane; complete profile
  admission gaps are recorded above. Fix those on available
  infrastructure; they are not waived as production-only work.
- amd64 Linux PC: optional separate live lane using the explicit smaller profile,
  hardware virtualization and qualified bounded storage/networking.
- Larger production/HA lane: future work on the infrastructure described in
  [the infrastructure guide](operations/rc-infrastructure.md). Encrypted snapshot
  restore, cross-node durability, disruption/capacity and upgrade evidence belong
  to the capabilities actually enabled there.

Kind without Kata can exercise provider functionality but cannot establish a
hardware-virtualized secure boundary. Phase 3 substrate tests are not an end-user
release: Phase 6 remains the first standalone usable milestone.
