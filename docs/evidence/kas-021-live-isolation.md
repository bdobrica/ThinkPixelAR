# KAS-021 — physical Kata isolation on the ARM64 homelab

Date: 2026-09-20. Scope: the runtime boundary of `coding-homelab-arm64`,
not complete secure-profile admission or production qualification.

A fresh bounded probe used the pinned [smoke manifest](../../deploy/kata/smoke.yaml),
renamed `kata-isolation-kas021`, selected `k3spi-02`, and reduced its deadline and
sleep to 900 seconds. The existing restricted namespace `thinkpixelar-kata-check`
and all-Pod deny-all policy remained in force. Requests/limits match the homelab
profile; workload privilege, token, host device and namespace access are disabled.
Objects are retained; the deadline bounds runtime consumption.

## Independently correlated proof

The trusted-worker [host probe](../../test/security/kata-host-proof.py) matched:

- Pod UID `a7129162-4193-4919-95f9-5f21a2b00d4c`;
- ready CRI sandbox `1246d2a53f45a088867608b2e599fb8a255751ca8ffdf7468298e9e37ce56ad2`;
- handler `kata-qemu-runtime-rs-ar331`;
- QEMU PID `17753`, exact `sandbox-<CRI ID>` name, `accel=kvm`, and an open
  `kvm-vm` descriptor in the host process;
- running executable SHA-256
  `e81b15b3da14bcbde77be46c322ab97b93c6146f903fe6c6bb8522aa675f66fa`;
- selected guest kernel SHA-256
  `a44d663f4ddad20a35527a3578fadef9beb23c1e5cb720e85d6928d6de70d3a1`;
- selected guest image SHA-256
  `7ebd652760c881374c0a761d34addcb76d9a650e35c10c01b780ebcdd9a1f2aa`.

CRI identity and QEMU process start time were rechecked after artifact hashing.
The controlled guest fixture additionally observed Linux `6.18.28` / `aarch64`,
UID/GID 65532, zero effective capabilities, `NoNewPrivs=1`, `Seccomp=2`, no API
service-account token and no `/dev/kvm`. Host kernel is `6.18.39+rpt-rpi-v8`.
The host KVM descriptor, not the guest's self-report, establishes hardware-backed
execution. Installed tuple: K3s `v1.36.4+k3s1`, containerd `2.3.4-k3s1.36`, Kata
`3.31.0` with the reviewed ARM64 operator overrides.

## Reproduction and limits

Follow the [Kata guide](../operations/kata.md#live-host-correlation). Exact pins
intentionally reject other artifacts, software emulation, wrong Pod identity,
wrong handlers, ambiguous processes and missing live KVM evidence. This is an
operator qualification tool, not the production infrastructure verifier. Root
access stays on the trusted worker and never enters the workload.

The [installation evidence](kata-homelab-installation.md) covers all three worker
runtime checks. [Controller lifecycle](kas-019-live-lifecycle.md) and
[native suspend/resume](kas-020-live-suspend-resume.md) independently exercise the
same handler through the AR adapter. This focused runtime probe does not claim
storage/process/ephemeral bounds, amd64 qualification, runtime exploit resistance,
or authenticated agentd readiness. The runtime mapping remains a candidate until
its resource overhead and other required bounds are qualified.

Validation: live host correlation passed; negative parser tests reject emulation,
identity mismatch and ambiguous guest artifacts; `make verify` passed.
