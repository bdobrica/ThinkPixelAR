# Homelab release-candidate infrastructure

The RC targets evaluation on existing hardware, with no required cloud spending.
Phase 3 uses the maintainer's three ARM64 K3s workers and installed Kata runtime.
A compatible Linux PC is another intended evaluation environment; its exact
amd64 tuple needs its own smoke run before being described as tested.

## Evaluation lane

Use an explicit architecture-matching profile:

- [coding-homelab-arm64](../profiles/coding-homelab-arm64.json)
- [coding-homelab-amd64](../profiles/coding-homelab-amd64.json)

Each requests 250m CPU / 256 MiB workload memory and limits the workload to one CPU /
512 MiB, with 64 MiB requested / 128 MiB limited ephemeral storage, 128 processes
and a 1 GiB Workspace. Guest/runtime overhead is additional and must be measured
and reserved; these numbers are not the total host requirement. Begin with one
sandbox per worker. The published profiles remain candidates until live enforcement
checks pass; application admission must reject any unimplemented bound.

Keep Kata hardware virtualization, restricted Pod settings and default-deny
networking. The offline network profile permits only required DNS/control traffic;
it does not promise a configured model provider or external package access.

Local single-writer storage is sufficient for the evaluation lane. Snapshots and
fork are disabled. No at-rest encryption or node-loss durability is advertised.
Retained local Workspace data can survive compute replacement on the same node;
a lost disk/node can make it unavailable. Keep evaluation data appropriate to
these limits and handle unsupported recovery explicitly.

The current concrete Kata operator values target ARM64 K3s. On an amd64 PC,
install the same pinned runtime with the official platform-appropriate artifacts,
configure an amd64 operator mapping and run the same live checks. Do not apply
the ARM64 wrapper fixes to x86 binaries. This is not a claim that arbitrary desktop
VMs provide nested KVM: verify `/dev/kvm` on the actual Kubernetes worker.

## Future production qualification — not an RC prerequisite

For the unchanged `coding-medium-secure` profile, provide:

- Linux amd64 Kubernetes workers with hardware virtualization exposed to Kata.
  A practical test starting point is at least 8 CPU cores and 32 GiB RAM per
  worker, leaving capacity for the 4 CPU / 8 GiB workload limit, measured guest
  overhead and system services. Derive actual concurrency from measurements.
- At least two suitable workers to test node replacement; three when testing
  quorum/distributed storage behavior. Separate control-plane capacity from
  hostile workloads where the deployment requires that availability boundary.
- Enough usable local ephemeral disk for each 20 GiB workload limit plus image
  layers, runtime artifacts, logs and Kubernetes eviction reserve. Start with
  at least 200 GiB usable SSD space per worker for repeatable multi-sandbox tests.
- A qualified storage implementation providing at least 50 GiB Workspace capacity,
  encrypted data at rest, single-pod-writer semantics, snapshots, restore/clone
  and cross-node attachment. Record driver/sidecar/controller versions, actual
  encryption/key ownership, snapshot consistency and retention behavior.
- Enforced IPv4/IPv6 default-deny networking, exact control/gateway exceptions,
  metadata/API denial, DNS/rebinding protection for enabled network modes, and
  trusted gateways where application-layer destination identity is required.
- The pinned Agent Sandbox/Kata/containerd tuple, externally verified process and
  cgroup/storage limits, realistic concurrent load and disruption/backup/restore
  tests. Include node loss, storage loss, API partitions and runtime upgrades.

These are requirements for claiming the corresponding production guarantees.
They are not a request to purchase hardware now, and their absence does not
invalidate accurately scoped homelab RC results. Track tested guarantees in phase
and release evidence; do not promote a candidate tuple through documentation alone.
