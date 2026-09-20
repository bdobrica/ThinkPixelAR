# KAS-020 — Native suspend/resume

Date: 2026-09-20. Agent Sandbox v1.0.0 core controller, ARM64 homelab worker
`k3spi-02`, Kata 3.31.0 runtime-rs/QEMU.

`make test-kas-suspend-resume` passed with the race detector in 29 seconds through
the documented loopback API tunnel. Retained fixture namespace:
`ar-live-01a0c017-bad7-7d21-a56c-b390eeac575f`.

The adapter exercised:

- Native readiness with AR secure readiness correctly withheld.
- Repeated Suspend with one stable operation identity.
- Observed SUSPENDED only after the workload Pod disappeared.
- Repeated Resume with a second stable operation identity.
- The same Sandbox UID before and after resume.
- A different Pod UID after resume, proving compute/process replacement.
- The unchanged original absolute shutdown deadline; resume does not renew authority.
- Subsequent repeated release, confirmed absence and a fresh-identity replacement.
- Retained Workspace/vendor-state PVCs after release.

The fixture has no actual Execution/bootstrap credentials. Its native resume does
not prove a complete Session checkpoint/restore or authenticated harness startup.
Those remain application workflows under the existing suspend/resume contract.
Template/network callbacks are explicitly test-only; no effective verifier is
installed and no secure READY claim is made.

Verification: live `make test-kas-suspend-resume` with explicit loopback API, node
and RuntimeClass configuration; `make verify`. Installation and test reproduction
are in the [operator guide](../operations/agent-sandbox.md). Test resources are
retained except for the compute explicitly removed by lifecycle operations.
