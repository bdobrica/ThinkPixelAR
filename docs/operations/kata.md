# Kata on the ARM64 K3s homelab

This procedure adds a separately selected hardware-virtualized runtime. It does
not change the cluster's default `runc` handler. Agent workloads select
`kata-qemu-runtime-rs-ar331`; only explicitly labeled ARM64 nodes can receive
them. The initial canary is worker `k3spi-02`, not the control-plane node.

## Installation inputs

- Kata: `3.31.0`, QEMU with Rust runtime (`qemu-runtime-rs`).
- Official chart: `kata-deploy-3.31.0.tgz`, SHA-256
  `528a6fb55c65c2df003cdf56e1ee7b7e89399046c5c99ef949ae1d409e72ddf2`.
- Installer image: `quay.io/kata-containers/kata-deploy@sha256:643471067559cbde3258a62eb60840ce435383d9a6dae7a803bd9418eb81be8b`.
- ARM64 manifest: `sha256:a29d7a375876eb05684df687192f501385aa5de6d1fab527722c06af610137ab`.
- [Operator values](../../deploy/kata/values.yaml) are the source definition;
  render the chart rather than editing its generated manifest.
- Helm used for rendering: `v3.19.0`; Linux amd64 archive SHA-256
  `a7f81ce08007091b86d8bd696eb4d86b8d0f2e1b9f6c714be62f82f96a594496`.

Source: [official Kata release](https://github.com/kata-containers/kata-containers/releases/tag/3.31.0)
and its bundled chart/values. Artifacts are downloaded by exact release and
checked against the above hashes. Neither chart nor installer uses `latest`.

## What changes on a node

The official `kata-deploy` installer copies the Kata shim, QEMU, guest kernel,
guest root filesystem and configuration into its suffixed host installation.
It adds a containerd runtime handler using K3s's configuration path and reloads
the node runtime. The Rust shim starts one QEMU guest per sandbox Pod; containers
inside the Pod use the guest kernel. Kubernetes credentials remain outside that
Pod. No device passthrough or confidential-compute claim is made.

The installer is trusted infrastructure: its DaemonSet requires a privileged
container, host PID visibility and host filesystem mounts to configure the node.
This is not the security context of an agent Pod. Its dedicated ServiceAccount
can inspect/label nodes and manage runtime installation metadata; do not reuse
it for AR or sandbox workloads. NetworkPolicy and resource/credential restrictions
remain required independently of Kata.

K3s `v1.36.4+k3s1` generates containerd schema version 3 and imports drop-ins
under `/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/`.
Never hand-edit generated `config.toml`. Review installed files and retain
backups before subsequent changes. The chart has no extra snapshotter or NFD
installation and creates no default RuntimeClass alias.

## Reproduce the canary rollout

Inspect existing workloads, available memory/disk, `/dev/kvm`, host kernel KVM
support, and existing RuntimeClasses first. Record Pod restart counts before
installation. The initial environment has four Raspberry Pi ARM64 nodes, K3s
`v1.36.4+k3s1`, and containerd `2.3.4-k3s1.36`.

```sh
helm template thinkpixel-kata ./kata-deploy-3.31.0.tgz \
  --namespace thinkpixel-kata --no-hooks \
  -f deploy/kata/values.yaml > install.yaml
kubectl create namespace thinkpixel-kata --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --dry-run=server -f install.yaml
kubectl label node k3spi-02 thinkpixel.io/kata-install=3.31.0
kubectl apply -f install.yaml
kubectl -n thinkpixel-kata get pods -o wide
kubectl -n thinkpixel-kata logs -l name=kata-deploy-ar331
kubectl get runtimeclass kata-qemu-runtime-rs-ar331 -o yaml
```

Homelab access is `ssh pi@k3spi` followed by `sudo kubectl`. No kubeconfig or
SSH private key belongs in the repository. The rendered manifest is retained on
the controller under `/home/pi/thinkpixelar-kata-331/install.yaml`.

The `--no-hooks` render deliberately excludes cleanup Jobs: installation resources
are retained. Expand only after the canary passes: label one additional worker,
wait for installation and guest validation, then proceed to the next. Leave the
4 GiB control-plane node out of the runtime pool unless separately qualified.

## Validation and limits

A RuntimeClass object alone does not prove isolation. Require a running test Pod,
its effective runtime handler, a guest kernel distinct from the host kernel,
trusted host QEMU/KVM evidence, and successful node runtime diagnostics. Inspect
resource cgroups, filesystem mounts, token absence, capabilities and egress
separately for the secure Runtime Profile. Keep functional installation evidence
separate from the full KAS-021/022 secure-profile qualification.

The sample coding profile's 8 GiB memory limit, guest overhead, process limits,
50 GiB Workspace and storage/network requirements must be checked against actual
node capacity and CSI/CNI support. Do not silently reduce that profile to fit a Pi.
ARM64 homelab evidence does not qualify the initial amd64 production lane.

## Rollback and retained resources

Do not automatically tear down this homelab. The official installer performs
host cleanup on SIGTERM; deleting its DaemonSet or removing its node selector
label can uninstall Kata and restart the node runtime. Treat these as maintenance
operations, not harmless cleanup of test Pods.

For an explicitly requested rollback: stop scheduling new Kata Pods, checkpoint
and release only the affected AR workloads, verify no Kata guests remain, then
follow the pinned chart's cleanup procedure one node at a time. Keep RBAC alive
until installer cleanup finishes, confirm ordinary runc workloads and the node
are healthy, and only then remove unused RuntimeClasses/RBAC. Do not touch
ThinkPixelAG namespaces, retained data or its fenced historical writers.

## ARM64 multi-install and seccomp corrections

The pinned installer's `ar331` prefix generator emitted an x86 QEMU wrapper and
nonexistent generic firmware paths on ARM64. Its runtime-rs defaults also left
virtiofsd under `/opt/kata` and disabled guest seccomp. The first probes exposed
these failures; they were not accepted as successful qualification.

After each node's installer is Ready, copy the three files from `deploy/kata/`
(`install-arm64-overrides.sh`, `90-arm64-paths.toml`, and
`qemu-system-aarch64-ar331`) together onto that node, then run:

```sh
sudo sh ./install-arm64-overrides.sh
```

The script checks architecture, the exact runtime version and required artifacts;
installs an operator-owned QEMU wrapper with the correct `-L` ROM search path;
and adds `90-arm64-paths.toml` beside the generated prefix drop-in. That file
selects the shipped ARM64 QEMU/kernel/image/firmware/virtiofsd paths, disables
workload-controlled hypervisor annotations, and sets
`[runtime] disable_guest_seccomp = false`. It does not modify the generated
`10-installation-prefix.toml` or vendor binaries. `kata-runtime check` runs against
the actual suffixed configuration; its root-mode network check is skipped by
upstream, so network probes remain separate.

Only then label the node for scheduling:

```sh
kubectl label node NODE thinkpixel.io/kata-configured=3.31.0
```

The RuntimeClass requires both the installation and configured labels. Repeat
this procedure and guest tests after reinstall/upgrade; do not assume a vendor
installer preserves operator corrections. Remove the configured label before
maintenance so no new workloads reach a partially installed runtime.

Apply [smoke.yaml](../../deploy/kata/smoke.yaml) and inspect its logs. For each
additional worker, render a distinct Pod name with
`spec.nodeSelector.kubernetes.io/hostname` set to that worker; retain namespace
and NetworkPolicy. Expect `Linux 6.18.28 aarch64`, UID/GID 65532,
`CapEff: 0000000000000000`, `NoNewPrivs: 1`, and `Seccomp: 2`. The command also
requires absence of the ServiceAccount token and `/dev/kvm` inside the guest.

Initial probe records `kata-guest-check` and `kata-guest-check-v2` were retained
and ended through `activeDeadlineSeconds: 1` to release their guest memory. Their
Error status is expected diagnostic history. The corrected probes use v3 and
worker-specific names. No automatic resource deletion is performed.

## Installed result

All three workers passed guest probes on 2026-09-20; the control plane remains
excluded. See [installation evidence](../evidence/kata-homelab-installation.md)
for exact observations and artifact hashes. The observed QEMU guest allocation
was 2560 MiB for the small probe, while the chart advertises only 160 MiB fixed
memory overhead. Measure actual host/guest accounting before production
admission; neither that overhead nor the secure coding profile is qualified yet.

## AR runtime-reference mapping

[Runtime mapping](../../deploy/kata/runtime-mapping.json) binds the abstract
`kata-qemu-3-31` reference to this installation. Feed operator mappings into the
adapter's `NewKataRuntimeResolver`; its `Resolve` reads the actual RuntimeClass
and returns a digest of the mapping, observed UID and independent qualification
evidence. This library is not yet an automatically enabled service configuration.
The qualification callback must use trusted host/guest evidence; returning a
digest because the RuntimeClass exists is invalid.

The supplied mapping is ARM64-only and intentionally cannot admit the existing
amd64 coding profile. Its candidate qualification reference and installer-default
overhead remain unqualified. A production mapping requires completed evidence and
must be combined with the separate storage/network admission checks.

## Live host correlation

For each runtime qualification, create a fresh bounded probe from
`deploy/kata/smoke.yaml`: choose a unique Pod name, pin the target worker with
`nodeSelector.kubernetes.io/hostname`, and use a short deadline (900 seconds is
sufficient). Apply its namespace and deny-all policy before the Pod. Wait for
readiness and obtain its UID from the trusted Kubernetes API. Keep the objects
for evidence; the active deadline stops the probe.

On that worker, run the checked-in read-only tool through your operator SSH
connection (substitute the exact Pod name, namespace, UID and handler):

```sh
sudo python3 kata-host-proof.py --name "$PROBE_NAME" \
  --namespace "$PROBE_NAMESPACE" --uid "$PROBE_UID" \
  --handler kata-qemu-runtime-rs-ar331
```

The tool matches the ready CRI sandbox to QEMU, verifies pinned executable/kernel/
image hashes and an actual host KVM VM descriptor, then rechecks identity. It is
specific to the recorded ARM64 artifacts. Review/update pins and rerun qualification
when changing artifacts; do not simply bypass mismatches. It prints selected
infrastructure facts only. It does not certify the complete profile or supply
production `EffectiveVerifier` evidence. See [KAS-021](../evidence/kas-021-live-isolation.md).

Resource diagnostics, the canary process-limit installation and current scratch
enforcement failures are documented in [the resource guide](kata-resource-checks.md).
