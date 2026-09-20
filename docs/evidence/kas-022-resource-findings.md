# KAS-022 — resource findings and remaining enforcement gap

Date: 2026-09-20. **KAS-022 remains incomplete.** These are bounded diagnostic
results on the existing ARM64 homelab, not a qualified complete Runtime Profile.
No additional infrastructure was purchased or requested.

## Tested tuple and changes

K3s `v1.36.4+k3s1`, containerd `2.3.4-k3s1.36`, Kata `3.31.0`, worker `k3spi-02`,
namespace `thinkpixelar-kata-check`, restricted Pod security and deny-all network.
The workload image is the pinned BusyBox digest in `deploy/kata/smoke.yaml`.
Resource requests were 250m CPU, 256 MiB memory and 64 MiB ephemeral storage;
limits were 1 CPU, 512 MiB and 128 MiB. Scratch `emptyDir.sizeLimit` was 32 MiB.

Two explicit diagnostic handlers were added on worker02 only. The runtime-rs
handler uses a generated OCI base specification with PID and hard process rlimits.
The Go-handler comparison uses the shipped 3.31.0 Go shim and generated configuration
with `disable_guest_empty_dir=true`, reviewed ARM64 paths, guest seccomp and no
workload hypervisor annotations. Neither handler is selected by the checked-in AR
runtime mapping. Both RuntimeClasses reserve 250m/2304 MiB overhead as a conservative
experiment; this is not measured capacity qualification. QEMU's observed 2560 MiB
guest allocation includes its 2048 MiB base plus the 512 MiB container envelope.

## Results

| Check | Observation | Conclusion |
| --- | --- | --- |
| Trusted OCI limits | CRI inspection showed CPU shares 256, quota/period 100000/100000, memory 536870912, PID limit 128 | Desired settings reached the runtime; not sufficient alone |
| Guest CPU | `cpu.max=100000 100000`; eight seconds of parallel load increased throttled periods 18→98 and usage by about 8.05 CPU seconds | One-core throttling demonstrated on runtime-rs |
| Guest memory | `memory.max=536870912`; capped 640 MiB allocation was killed; `memory.events` showed `oom=1`, `oom_kill=1`; Pod eventually reported `OOMKilled` | 512 MiB guest ceiling demonstrated; OOM did not immediately terminate every container process |
| Guest PID cgroup | `pids.max=max` despite OCI limit 128 | PID cgroup bound failed |
| Hard process rlimit | Soft/hard 128; 122 children started before EAGAIN; raising hard limit to 256 returned EPERM | Alternative ceiling passed on both diagnostic runtimes; ADR-0019 |
| runtime-rs disk scratch | 48 MiB written into 32 MiB `emptyDir`; no eviction within 150 seconds; kubelet reported only 4096 bytes for the volume | Boundary/accounting failed |
| runtime-rs memory scratch | 48 MiB written; guest tmpfs capacity about 1.2 GiB rather than 32 MiB | Boundary failed |
| Go memory scratch | Same oversized guest tmpfs; host tmpfs was 32 MiB and empty | Boundary failed; host-only limits are not evidence of guest enforcement |
| Go disk scratch | 48 MiB written, then `Failed/Evicted`: `Usage of EmptyDir volume "tmp" exceeds the limit "32Mi".` | Asynchronous eviction demonstrated, not an immediate hard quota |
| Go privilege comparison | Initial process: capabilities zero, NoNewPrivs 1, seccomp 2; operator CRI exec: NoNewPrivs 0 | Do not promote the comparison tuple without reviewing exec semantics |

Retained probes: `kata-bounds-kas022`, `kata-bounds-rlimit-kas022`,
`kata-resources-kas022` (UID `ec29a397-67d7-41b1-a4a7-0105fac3d92e`),
`kata-memory-kas022` (UID `84a13b5e-ae10-41a2-a92b-12e2c2058af0`),
`kata-tmpfs-kas022`, `kata-go-tmpfs-kas022`, and `kata-go-disk-kas022`.
All probes have finite active deadlines. Completed diagnostic Pods were stopped
by shortening their deadlines while retaining their API objects. All four nodes
remained Ready after the sequential worker02 agent restarts.

## Required next work

Provide and test a physically bounded scratch implementation before completing
KAS-022 or promoting secure readiness. Options include a qualified runtime fix
that preserves tmpfs size, or a quota-backed attachment whose actual guest mount
is independently verified. Do not weaken the hard resource contract to call
asynchronous eviction a hard filesystem quota. Qualify actual VM/host overhead
under load before replacing the candidate runtime mapping. Local-path PVC capacity
is likewise an allocation request, not a filesystem quota; durable bounded storage
must be provided by the Workspace implementation before admission.

This is an observed enforcement failure on available hardware, not a request for
larger/paid infrastructure or a deferred production-scale qualification. The
original larger amd64/encrypted/snapshot lane remains future work under ADR-0014.

## Verification

The [resource probe](../../test/security/resourceprobe/main.go) was cross-compiled
for ARM64 and run live with bounded modes. It creates at most 150 short-lived
children, attempts at most 640 MiB memory and writes at most 48 MiB. The documented
runtime-rs installer was rerun idempotently against worker02. Focused Go race tests,
Python host-proof tests and `make verify` passed. Failed live checks above remain
failures regardless of the passing repository gate.
