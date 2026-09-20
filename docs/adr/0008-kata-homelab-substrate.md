# ADR-0008: Install an explicitly selected Kata runtime on homelab workers

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Phase 3 requires real hardware virtualization evidence. The authorized homelab
has three ARM64 workers with KVM and a smaller control-plane node. Existing
ThinkPixelAG workloads and retained validation resources must be preserved.

## Decision

Install pinned Kata 3.31.0 using the official digest-pinned kata-deploy chart
image, with QEMU/runtime-rs, an isolated `ar331` installation prefix and explicit
RuntimeClass `kata-qemu-runtime-rs-ar331`. Keep runc as the cluster default.
Roll out one worker at a time; exclude the control-plane node. RuntimeClass
scheduling requires operator-controlled installation and configuration labels.

Keep ARM64 corrections in an operator-owned drop-in and wrapper: select shipped
ARM64 artifacts, set QEMU's ROM directory, disable workload hypervisor annotation
overrides, and enable guest seccomp. Do not edit generated vendor configuration.
Apply corrections before adding the configuration label. Trusted installation
privileges never become workload privileges.

## Alternatives considered

Installing on every node would consume control-plane capacity unnecessarily.
Using the unsuffixed default runtime would increase interference with existing
workloads. Accepting upstream defaults failed actual ARM64/guest-seccomp probes.
Software emulation would not prove hardware isolation.

## Consequences

The homelab supports real Kata experiments. Its ARM64 installation is not a
qualification of the amd64 coding profile or its complete storage/network and
resource guarantees. Operator corrections must be reapplied and verified after
reinstallation. This is an operational prerequisite, not public API behavior.

## Security

Workload probes use restricted Pod security, no API token or host KVM device,
non-root execution, dropped capabilities, read-only root, seccomp and deny-all
network policy. These controls and the guest boundary require separate effective
verification. Kata does not grant authority or replace gateway enforcement.

## Operations

Retain manifests and probe history. The installer can restart the node runtime;
check health between nodes. Its removal can uninstall the handler, so rollback
is explicit maintenance. Remove the configuration label before runtime changes.
Measure guest and host memory before admitting production profiles: the shipped
RuntimeClass overhead is not demonstrated to cover the observed guest allocation.

## Compatibility

Evidence applies to K3s v1.36.4+k3s1, containerd 2.3.4-k3s1.36 and the exact ARM64
artifacts in the evidence record. The broader supported-version matrix remains
candidate-only until the full gates pass. Concrete handler names stay in the
operator/adapter boundary.

## References

- [Installation runbook](../operations/kata.md)
- [Live evidence](../evidence/kata-homelab-installation.md)
- [Supported versions](../supported-versions.md)
