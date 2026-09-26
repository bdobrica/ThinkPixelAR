# CDX-016: native Codex in a Kubernetes Agent Sandbox

Date: 2026-09-26. The real pinned Codex 0.155.0 driver ran on ARM64 hardware
inside a controller-created Kata Pod. Reproduce with the
[probe guide](../../test/e2e/codex-sandbox/README.md).

## Observed execution

- Cluster: K3s `v1.36.4+k3s1`, Agent Sandbox core `v1.0.0`, controller index
  `sha256:bdde1a3150bd385f7318c974c1516e880b4f826b6b51a3e7f127c2f8c95b55cd`.
- Namespace `ar-cdx016-20260926`, Sandbox `codex-probe`, UID
  `6b612da2-c7ea-4426-868d-9892236b6c7c`.
- Controller-owned Pod `codex-probe`, UID
  `249d2eaf-5486-47be-8505-2b50cef96555`, worker `k3spi-02`;
  started `2026-09-26T14:00:59Z`, zero restarts at observation.
- Runtime handler `kata-qemu-runtime-rs-ar331`; guest kernel `6.18.28 aarch64`,
  host kernel `6.18.39+rpt-rpi-v8`.
- Existing read-only host proof correlated that exact Pod UID to CRI sandbox
  `cd561fa3edb9fd262474b07cce007cc0c924e72f1c317bc1d6836635168244ed`, QEMU PID
  `8993`, `accel=kvm` and an actual KVM VM descriptor. QEMU/kernel/root-image
  hashes matched the checked-in [host-proof pins](../../test/security/kata-host-proof.py).
- Guest assertions passed: UID 65532, zero effective capabilities,
  NoNewPrivs=1, seccomp=2, no service-account token or `/dev/kvm`.
  Effective Pod had read-only root, no privilege escalation, only two
  memory-backed writable volumes, no host mounts and no Service. Namespace
  deny-all ingress/egress policy was created before the workload.

## Artifacts

The local probe image derives from CDX-002's verified multi-platform index
`sha256:184827fafb52373e4aec2002ae3a37c2ad3a7dc69388fb9e744549a73b769739`.
Agentd was rebuilt from `10e1080` source; tests include this CDX-016 change.
Go 1.26.7, CGO disabled, Linux ARM64; agentd uses `-trimpath -buildvcs=false`
and `-ldflags='-s -w'`. The qualification image adds test binaries and changes
its entrypoint to the probe script. No registry publication occurred.

| Artifact | SHA-256 |
| --- | --- |
| Probe OCI index | `83a9e9792dcbf3cdeb2e399054e5878b3fdf4ee210e4b466471586a8751cf8f6` |
| ARM64 manifest | `bcb395bbcec8aaa69263ec9af8a461682d98a5a7e54eb9a104a35be95e17f469` |
| ARM64 Codex executable | `98a3ca0f4edf0e6afccf73cba2f129dc71c7992638f94a78a36f34fe01a019d7` |
| Agentd executable | `4d5a4c4e81b9cffdcbcc3d557b5f3fc43f2ee05c3cd46d82c49368abe08f14a4` |
| Codex test executable | `c5c9ebd7503b38654c93040e401148dfb696a17bd5ac5dacd08e565c9cb5588b` |
| Agentd test executable | `be0b0b97af7b1e0bbcf49c00bb498795f2632673a2b418cbceb7f0e3824aa3de` |

The workload selected the exact probe index with pull policy Never after a direct
worker import. Containerd reported the Docker-save outer archive index
`2029b27b1628629c506c21e9c7113d4478d3367c6894bfbf39a18b1ceb631918` as imageID;
rehashing its JSON verified that it references the above probe index, which
references the above ARM64 manifest. The executable and schema pins were then
verified inside the running guest, not inferred from the image tag.

## Checks and limits

The native guest passed all selected tests:

- `TestPinnedCodexSupervisedStartup`: real agentd process-control handshake,
  thread identity, turn acceptance, START replay, disconnect/reap (9.60 seconds).
- `TestPinnedAppServer`: exact executable/version and 312-file schema digest,
  initialize/initialized and rejection checks (4.94 seconds).
- `TestPinnedTurnEvents`: ordered start/message/usage/completion candidates
  from a real Codex turn using loopback Responses SSE (5.03 seconds).
- `TestPinnedTurnFailure`, `TestPinnedTurnInterrupt`, `TestPinnedTurnStart`:
  failure normalization, interrupted terminal observation/replay and acceptance
  replay (4.77, 4.72, 5.03 seconds).
- Final log marker: `CDX-016 PASS: real pinned Codex supervisor, turn, candidates and interruption`.

Focused local amd64 race tests and `go vet` for Codex/agentd passed. ARM64 test
binaries and agentd compiled; shell syntax, renderer rejection of mutable images,
server dry-run and actual Sandbox creation passed. An initial dry-run rejected
an incorrect `replicas` field; the renderer now uses pinned v1 `operatingMode`.

This closes CDX-016's native sandbox-execution scope. It is an operator-run
qualification fixture, not application admission or a production deployment.
Supervisor turn acceptance and direct-driver completion are separate checks;
model data is synthetic loopback SSE. No paid provider, LLMGW/TG/AG integration,
authenticated event delivery, durable Session/Workspace continuation, generic
HarnessAdapter conformance, malicious-repository qualification or secure-profile
resource qualification is claimed. Those retain their existing backlog scope.
The Sandbox expires after twenty minutes, removing its Pod while retaining the
Sandbox/namespace/policy; logs and selected observations were captured beforehand.
