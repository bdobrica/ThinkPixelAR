# KAS-019 — Live cold lifecycle

Date: 2026-09-20. Existing ARM64 K3s/Kata homelab, worker `k3spi-02`.
Installed exact core Agent Sandbox v1.0.0 with the digest-pinned, bounded controller
[operator overlay](../../deploy/agent-sandbox/kustomization.yaml).
[Reproduction and pins](../operations/agent-sandbox.md).

The first live adapter run passed in 22 seconds. Retained namespace:
`ar-live-01a0c012-8c79-7b12-81e4-0b3f56a0d086`.

Verified against the real API/controller/Kata Pods:

- Acquisition and adapter re-instantiation replay return the same Sandbox UID.
- Native Ready is reached with current observed generation.
- AR refuses secure READY without an effective-state verifier.
- Repeated exact release succeeds and foreground deletion reaches actual absence.
- Acquisition cannot recreate a released, previously bound identity.
- A replacement Attempt/Sandbox uses a fresh UID and reaches native readiness.
- Both Workspace and vendor-state PVCs survive compute release.

No namespace/volume teardown runs. Sandbox/Pod deletion is the explicit lifecycle
operation under test. The controller installation and its retained operator files
are documented; no cluster credentials are committed or copied to Pods.

Verification: explicit live `TestLiveColdLifecycle` through the loopback SSH API
tunnel; `make verify`. Ordinary unit runs skip the live fixture without its explicit
environment. Native readiness is not full physical profile qualification, and
fixture memory bindings do not replace the separate real-PostgreSQL evidence.
