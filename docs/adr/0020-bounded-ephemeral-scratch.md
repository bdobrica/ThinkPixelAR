# ADR-0020: Use physically bounded Pod-owned scratch claims on the homelab

- Status: Accepted
- Date: 2026-09-21
- Deciders: ThinkPixelAR maintainers
- Supersedes: ADR-0013's tmp-volume source restriction only; ADR-0019's canary-only runtime selection for the tested resource lane
- Superseded by: None

## Context

Kata 3.31.0 did not preserve the tested emptyDir capacity. The Go-runtime comparison
provided disk accounting and eviction, but eviction is not a hard filesystem quota.
The homelab has existing SSD space and needs no new paid infrastructure.

## Decision

Allow the trusted coding mapper to select a generic ephemeral PVC for `/tmp`.
Its StorageClass and byte capacity are part of the immutable implementation
snapshot. Permit only a fresh RWO filesystem claim with an explicit qualified class
and one bounded storage request. Reject clone/snapshot sources, existing volume
selection, arbitrary metadata and unreviewed fields. Keep the other three mount
roots, credential boundary and Workspace ownership unchanged. An emptyDir mapping
remains available only where its independent qualification actually proves the bound.

For the homelab test lane, use preallocated 32 MiB ext4 loop files on the existing
SSD, exposed as local PVs in `ar-bounded-scratch-v1`. The workload sees a filesystem
through Kata/PVC, never a hostPath, loop device, KVM device or privileged helper.
The qualified class has exactly 32 MiB backing slots; filesystem overhead reduces
usable capacity. A PVC capacity field alone never establishes the physical bound.

Kubernetes owns the per-Pod scratch claim. Retain the released PV and backing file;
never clear its claimRef or automatically reuse it. Resume/replacement takes fresh
scratch. Pool exhaustion leaves compute Pending. Durable Workspace/state PVCs stay
with their separate owner. Trusted proof validates exact Pod→PVC→PV identities and
actual backing/mount enforcement before secure readiness.

Select the runtime-rs bounded handler from ADR-0019 on worker02, with 250m CPU and
2304 MiB memory overhead. The resource stress measured its host/guest ceilings and
memory usage; this conservative reservation covers the 2 GiB guest base plus an
additional 256 MiB beyond the workload limit. Qualification is scoped to the tested
512 MiB/1 CPU homelab lane, not arbitrary density or production capacity.

## Alternatives considered

Trusting emptyDir size or asynchronous eviction repeats the observed failure.
A runtime fork adds build/upgrade burden. Giving the agent privileges to mount or
remount filesystems defeats the boundary. A bounded existing-disk allocation is a
small operator-side implementation and keeps storage replaceable.

## Security and operations

Generate mount units and k3s-agent dependencies before publishing each PV. Missing
backing storage must prevent the worker agent starting; it must not expose an
ordinary unbounded directory after reboot. The tool refuses existing files/mounts
and never reformats existing storage. Node-local storage is unencrypted, not HA,
and has no snapshots. Manual retained-data disposal/replenishment is explicit.

This proves the scratch filesystem boundary. Kubelet's general ephemeral-storage
accounting/eviction remains an additional operational control, not a claimed hard
filesystem quota. Complete image, networking, durable Workspace, bootstrap and
agentd proof remains mandatory before end-user secure admission under ADR-0013.

## Compatibility

No public profile/schema or cross-component ownership changes. The optional
adapter configuration field changes the implementation digest when selected.
Existing Execution snapshots cannot be substituted. Generic ephemeral PVCs are a
core Kubernetes mechanism; no extension controller or new dependency is required.

## References

- [KAS-022 fixed resource evidence](../evidence/kas-022-bounded-resources.md)
- [Operator setup](../operations/bounded-scratch.md)
- [Kubernetes generic ephemeral volumes](https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/)
