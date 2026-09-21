# KAS-022 — bounded homelab resource lane

Date: 2026-09-21. Completes KAS-022 for the explicitly tested ARM64 lane under
ADR-0014 and [ADR-0020](../adr/0020-bounded-ephemeral-scratch.md). The
[earlier failures](kas-022-resource-findings.md) remain valid for their configurations;
neither emptyDir nor the Go-runtime comparison was silently promoted.

## Implementation and environment

Existing worker02, K3s `v1.36.4+k3s1`, containerd `2.3.4-k3s1.36`, Kata `3.31.0`
runtime-rs/QEMU, pinned BusyBox image and reviewed ARM64 artifacts. The
`kata-qemu-runtime-rs-ar331-bounded` handler adds trusted hard process rlimits.
The [runtime mapping](../../deploy/kata/runtime-mapping.json) now selects this exact
worker/class and the measured resource lane with 250m/2304 MiB overhead.

Scratch uses Pod-owned generic ephemeral claims backed by fresh 32 MiB ext4 local
PVs on existing SSD storage. No hostPath/device/privilege enters the workload.
The operator generator reserves actual disk space, installs boot mount dependencies,
and refuses existing files. Retain prevents cross-Attempt data recycling.
The mapper records the storage class/capacity in its immutable implementation digest;
the verifier checks exact Pod→PVC→PV identity before independent physical proof.

## Physical results

Resource Pod `kata-hard-resources-kas022` in `thinkpixelar-kata-check`:
UID `cddfc146-a2bc-4458-83c5-6eea70c6a59d`, CRI sandbox
`0f1a3b6a9dc7b250040e8fc5f801e9ebd70f0055a9f68033cf537fc45e406314`.

| Check | Observed result |
| --- | --- |
| Scratch hard capacity | ENOSPC after 22020096 bytes of fill writes, with the probe binary and ext4 metadata also occupying the 32 MiB allocation. Guest `df` reported 25844 KiB filesystem capacity. |
| Aggregate capacity | A second file could not bypass exhaustion; removing the probe's fill file restored writable capacity. This is filesystem enforcement, not delayed eviction. |
| CPU request/limit | Guest `cpu.weight=10`, `cpu.max=100000 100000`; eight seconds of parallel load increased usage 2334926→10409889 µs and throttled periods 18→99. |
| Memory | Guest `memory.max=536870912`; capped allocation killed at the ceiling, exit 9; `memory.events` reported oom=1, oom_kill=1. |
| Processes | Hard/soft RLIMIT_NPROC=128; 122 children before EAGAIN; attempted hard-limit increase denied. Guest PID cgroup remains unlimited; enforcement is the tested rlimit, not that cgroup. |
| Workload security | Initial process UID/GID 65532, zero effective capabilities, NoNewPrivs=1, seccomp=2. |
| VM / host reserve | QEMU configured 2560 MiB / 2 vCPUs; host RSS high-water 738664 KiB after stress. Pod cgroup memory.max=2952790016 and cpu.max=125000/100000 include the declared overhead; memory.peak=554274816. RSS and cgroup charging are distinct metrics, not additive measurements. |
| Boot dependencies | systemd reported RequiresMountsFor and BindsTo for the prepared backing mounts before PV publication. Actual reboot/disaster recovery was not tested here. |

The 2304 MiB overhead reserves the 2048 MiB guest base plus a 256 MiB margin beyond
the 512 MiB workload limit. This is a conservative tested single-sandbox lane,
not a density benchmark. Original installer overhead of 160 MiB is not qualified.
General kubelet ephemeral accounting/eviction remains separate from the hard
scratch mount bound; no immediate aggregate quota for host-generated logs is claimed.

## Lifecycle and repeatability

Race-enabled cold acquire/readiness/release/replacement and native suspend/resume
passed using the new scratch configuration. The strengthened repeat also checked
fresh PV identity after each Pod replacement and deletion of the Pod-owned scratch
claim after release, while preserving Workspace/state PVCs.

- Cold repeat: namespace `ar-live-01a0c4de-ace2-7ffa-8328-97e17d17400c`, 31.77s.
- Suspend/resume repeat: namespace `ar-live-01a0c4df-28f8-733e-9e8e-d3a19781f6b3`, 32.27s.
- Resume changed scratch claim UID `9f9f2778-3ede-4766-973b-4bf065da20e1` to
  `f658e053-bae5-4953-b3fe-875cae0a0475`, and PV from slot `h` to `k`.
  Replacement used fresh slot `l`; no released backing was reused.
- Slots `a`–`l` and test objects are retained. Completed diagnostic Pods were
  stopped through their active deadlines. No existing storage was formatted.

Reproduce with [the operator guide](../operations/bounded-scratch.md). Tests reject
clone sources, existing volumes, default classes, widened capacity, wrong owners,
rebound PVs and failed physical qualification. No dependency was added.
Focused adapter/probe race tests, Python checks and `make verify` passed.

## Scope

This closes the physical scratch/process/compute resource gap for the chosen lane.
It does not implement a dynamic provisioner, qualify local-path Workspace capacity,
certify all host logging paths, run production/amd64 density tests, or replace the
full effective-state verifier. Live fixtures still withhold secure AR READY.
Durable Workspace, bootstrap, image/network proof and authenticated transport must
be composed before the Phase 6 end-user admission path. Future production infrastructure
remains explicitly described in [the RC guide](../operations/rc-infrastructure.md).
