# Bounded scratch on an existing homelab SSD

The tested lane is ARM64 worker02, Kata 3.31.0 runtime-rs with the
[bounded process handler](kata-resource-checks.md), and one concurrent test sandbox.
This setup adds no paid service and leaves runc/default storage unchanged.

## Prepare capacity

On the trusted worker, copy `deploy/storage/prepare-scratch.py` and run:

```sh
sudo python3 prepare-scratch.py \
  --name ar-scratch-unique-slot --node k3spi-02 > scratch-pv.json
```

The parent of `--root` must be an existing mounted filesystem; the default root is
`/mnt/ssd/thinkpixelar-scratch`. The tool allocates exactly 32 MiB (no sparse
capacity overcommit), formats only the newly created file, mounts ext4 with
nodev/nosuid and gives UID/GID 65532 access to the filesystem root. It refuses any
existing image/mount/configuration. A failed setup is retained for inspection and
is never automatically formatted or retried over existing data.

It creates a mount unit in `/etc/systemd/system/` and a per-slot drop-in under
`k3s-agent.service.d/` with `RequiresMountsFor` and `BindsTo`. Units are loaded and
started before a PV manifest is emitted. No k3s-agent restart is needed for adding
slots; the dependencies govern subsequent starts. Preserve these files and the
backing image together. Do not unmount live storage: loss of a required mount stops
the node agent. Missing storage must fail startup instead of exposing a directory.
Reboot/disaster recovery belongs to the later operational qualification lane.

From the operator context, review and apply:

```sh
kubectl apply -f deploy/storage/scratch-storageclass.yaml
kubectl create -f scratch-pv.json
```

Only publish fresh 32 MiB slots in this class. A larger PV could satisfy a smaller
PVC request in Kubernetes; the AR scratch verifier rejects capacity mismatch and
independent storage qualification must also reject misconfigured backing. Restrict
StorageClass/PV creation to trusted operators. No workload permissions are added.

## Select and verify

Set trusted `CodingTemplateConfig.ScratchStorageClass=ar-bounded-scratch-v1` and
`TempBytes=33554432`. Qualification must validate this configuration and exact
backing guarantees; do not provide an unconditional production callback. The
mapper emits a generic ephemeral claim template, not a raw host path.

`NewScratchVerifier` verifies Pod/PVC/PV identity, ownership, exact capacity and
fresh filesystem shape before calling the trusted physical verifier. Compose it
with the full effective-state verifier; it does not qualify runtime, network,
image or durable Workspace by itself.

Run the documented lifecycle commands with the additional variable:

```sh
THINKPIXELAR_TEST_SCRATCH_CLASS=ar-bounded-scratch-v1
```

Export it alongside the API/node/runtime variables from [the lifecycle guide](agent-sandbox.md).
Cold lifecycle consumes two fresh slots; native suspend/resume consumes three.
The tests verify fresh PV identities and scratch-claim deletion while keeping
Workspace/state PVCs. Use `kata-qemu-runtime-rs-ar331-bounded` for this lane.

For hostile writes, build `test/security/resourceprobe` for the node architecture
and run `disk-bound` inside a disposable restricted probe Pod with the same scratch
claim template. It verifies ENOSPC across two files and recovery after deleting its
own test fill file. CPU, PID and memory modes remain bounded as documented in the
[resource guide](kata-resource-checks.md). A full namespace policy precedes compute.

## Retention and limitations

The local PV uses Retain. Kubernetes deletes the Pod-owned PVC when the Pod is
deleted; the PV becomes Released and retains its claimRef and data. Never clear
claimRef and recycle it across Attempts. Replenish with a new uniquely named slot.
No available slot means Pending, with no emptyDir fallback. Completed diagnostic
Pods may retain claims until explicitly deleted.

This intentionally small manual pool is a substrate test/evaluation option,
not a dynamic storage controller or the durable WorkspaceProvider. Later service
composition can supply a qualified dynamic backend through the same adapter
boundary. Workspace/state local-path capacity is not a hard quota and is not
promoted by this scratch fix. Production encryption/snapshots and cross-node
recovery remain separate capabilities.
