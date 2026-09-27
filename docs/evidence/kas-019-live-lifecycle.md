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

## TAR-005 storage survival regression (2026-09-27)

`TestLiveColdLifecycle` now writes a marker before release and, after each of its
two Sandbox releases, confirms all execution Pods are absent, both independent
Workspace/vendor-state PVCs retain their UIDs and PV bindings, and the backing
PVs retain their UIDs. A fresh restricted, read-only Pod reads the exact original
Workspace bytes. The verifier Pod is removed before replacement compute starts.

Passed in 32.42 seconds on `k3spi-02`, `local-path`, RuntimeClass
`kata-qemu-runtime-rs-ar331-bounded`, using the existing pinned BusyBox image.
Namespace: `ar-live-01a0e21e-184a-7c8a-ba83-342457068a64`.
Sandbox UIDs: `c1aa8771-ade5-472a-9ea8-4ffb523a5d73` and
`e58120b0-3084-46d0-8a00-4b06ca16cbb3`.
Workspace PVC UID: `4103855d-858a-4c8e-ac07-623e52b9e33b`;
PV UID: `2a963e2c-7ee8-4d5b-bf2d-c15de0dcc6f0`.
The test namespace is explicitly removed after verification; the fixture itself
retains it for inspection. An initial run was interrupted to fix an unterminated
marker log line, and its isolated namespace was also removed.

Using the linked operations guide’s loopback tunnel (port 18015 for this run), reproduce from AR:

```sh
THINKPIXELAR_TEST_KUBE_API=http://127.0.0.1:18015 \
THINKPIXELAR_TEST_NODE=k3spi-02 \
THINKPIXELAR_TEST_RUNTIME_CLASS=kata-qemu-runtime-rs-ar331-bounded \
go test ./internal/adapters/sandbox/agentsandbox \
  -run '^TestLiveColdLifecycle$' -count=1 -v -timeout=10m
```

`TestAttachedBlueprintComposesWSReservedVolumes` separately verifies release and
replay through the WS descriptor attachment composition after authority denial:
only the exact Sandbox is deleted, storage API mutations fail the fixture, and
the retained attachment binding is unchanged. Both affected adapter packages'
tests and vet passed. This proves the AR provider boundary and real Kubernetes
storage lifetime; it does not exercise live WS HTTP/metadata, AG, or Session-close
orchestration. No production readiness qualification is inferred.

## TAR-006 replacement Workspace reattachment (2026-09-27)

The cold lifecycle fixture now requires the replacement to read exactly
`tar005-preserved` before appending `-continued`. It cannot recreate missing
content. After the second release, the independent read-only verifier must read
exactly `tar005-preserved-continued`. Both Sandbox and all Pods are absent before
replacement acquisition; the replacement has distinct Sandbox and Pod UIDs and
retains the original Workspace/state PVC UIDs and PV names. The fixture keeps
Session, Execution and Workspace identities stable while advancing Attempt.

The final live run passed in 33.82 seconds using the TAR-005 command above,
`k3spi-02`, `local-path` and `kata-qemu-runtime-rs-ar331-bounded`:

- Namespace: `ar-live-01a0e226-3f72-7b43-9aad-1f7c5113a3fa`.
- Sandbox UIDs: `425d486a-fa03-4cd9-8eab-97d4d56a1736` → `55a79941-0852-458e-adaf-3577aafbdb8a`.
- Pod UIDs: `722332ab-5ce3-45be-abc8-8039050a2093` → `c695e556-061d-4e1a-9b70-2a5d622f9330`.
- Workspace PVC UID: `9928a027-b20e-4659-9382-2359e6aa49ce`; PV UID: `0a9b0859-ed95-4772-a2d7-7da4bb0fd12a`.

The earlier run also passed (33.15 seconds); the final run adds an explicit
Workspace ID to the fixture correlation. Both isolated namespaces are removed
after verification. Focused tests and vet passed for the Sandbox and Kubernetes
Workspace adapter packages.

`TestAttachedBlueprintComposesWSReservedVolumes` also verifies that replacement
rejects a stale attachment reservation and a denied current WS lookup, then
reattaches the same claim exactly once across acquisition replay using the new
Attempt reservation. Storage mutations remain forbidden by the fixture.

This verifies provider reattachment and continued writes, not canonical-generation
restore, node failover, persisted Session recovery, or a running WS/AG integration.
The live fixture uses independently provisioned claims, memory bindings, and
explicit test startup commands. Production authority/fence checks are not replaced
by these fixture callbacks; native Ready still does not become AR secure READY.
