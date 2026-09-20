# Kata homelab installation evidence — 2026-09-20

Scope: KAS-008A, the user-authorized installation prerequisite. This is not
KAS-021/022 secure-profile or Phase 3 exit evidence.

## Installed substrate

| Component | Observed value |
| --- | --- |
| Eligible nodes | k3spi-01, k3spi-02, k3spi-03; ARM64, all Ready |
| Excluded node | k3spi-00 control plane, Ready |
| Kubernetes | K3s v1.36.4+k3s1 |
| Container runtime | containerd 2.3.4-k3s1.36 |
| Host kernel | 6.18.39+rpt-rpi-v8 |
| Kata runtime | 3.31.0, runtime-rs, shim commit ddb8a5de89891f12e1ce0013eb066a330b2988b9 |
| RuntimeClass/handler | kata-qemu-runtime-rs-ar331 |
| Guest kernel | 6.18.28 aarch64 |
| RuntimeClass overhead | CPU 250m; memory 160Mi; not capacity-qualified |

Installation artifact hashes and reproducible operator steps are in the
[runbook](../operations/kata.md). On worker 03, installed artifact SHA-256 values:

| Artifact under `/opt/kata-ar331/` | SHA-256 |
| --- | --- |
| bin/qemu-system-aarch64 | e81b15b3da14bcbde77be46c322ab97b93c6146f903fe6c6bb8522aa675f66fa |
| share/kata-containers/vmlinux.container | a44d663f4ddad20a35527a3578fadef9beb23c1e5cb720e85d6928d6de70d3a1 |
| share/kata-containers/kata-containers.img | 7ebd652760c881374c0a761d34addcb76d9a650e35c10c01b780ebcdd9a1f2aa |

## Observed checks

- Three installer Pods Ready, one per worker. All cluster nodes Ready afterward;
  existing non-completed infrastructure/ThinkPixelAG Pods were Running.
- `kata-runtime check` passed on all three workers against their actual suffixed
  runtime-rs configuration. Upstream skips root-mode network checks; these were
  not counted as network evidence.
- Running probes: `kata-guest-check-v3` on 02, `kata-worker01-check` on 01,
  `kata-worker03-check` on 03. Each printed guest kernel 6.18.28, UID/GID 65532,
  effective capabilities zero, NoNewPrivs 1 and Seccomp 2. Their commands require
  absence of the service-account token and `/dev/kvm`. The worker-03 probe also
  explicitly asserts the printed capability/seccomp values.
- Trusted host QEMU process used the shipped ARM64 binary and `accel=kvm` with
  the guest kernel/image. This supports actual hardware virtualization, beyond
  merely observing a RuntimeClass name.
- On worker 02, in-guest TCP probes to the cluster API and metadata link-local
  endpoint failed. This is limited deny-all connectivity evidence, not the
  complete secure-profile adversarial suite.
- Initial failed probes remain as diagnostic history. They were stopped using
  activeDeadlineSeconds, not deleted. Corrected probes sleep for one hour;
  the checked-in manifest additionally sets a one-hour Pod deadline.

## Corrections and qualification gaps

Initial installation generated x86 wrapper/firmware paths, an unsuffixed
virtiofsd path and disabled guest seccomp. The checked-in ARM64 wrapper/drop-in
fixed those observed failures; the generated prefix file remains unmodified.

The observed worker-03 QEMU process allocated 2560 MiB guest memory for a Pod
with a 512 MiB container limit. Guest allocation is not identical to resident
host usage, but the default 160 MiB overhead cannot be accepted as sufficient
without measurement. No production capacity claim is made.

The default coding profile is amd64 and has larger compute/storage requirements.
Its complete Workspace encryption/snapshot, process-limit, gateway egress,
resource enforcement and lifecycle gates remain open. Agent Sandbox controller
installation, AR composition and actual provider integration are separate work.
No credentials or raw workload payloads are stored in this evidence.
