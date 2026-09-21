# Kata resource diagnostics on the homelab

Historical emptyDir diagnostics; the fixed scratch/runtime-rs lane is documented
in [bounded scratch](bounded-scratch.md) and [KAS-022 closure](../evidence/kas-022-bounded-resources.md).
The Go comparison remains unqualified. Read the
[historical failures](../evidence/kas-022-resource-findings.md) before using these
assets. AR's mapping now selects the bounded runtime-rs resource lane; secure readiness
still requires complete independent proof.

## What was installed

Only worker02 received the following operator-owned additions:

- `install-bounded-handler.py` generates
  `/opt/kata-ar331/share/defaults/kata-containers/ar-bounded-oci.json` from the
  installed `k3s ctr oci spec`, with PID and hard `RLIMIT_NPROC` ceilings of 128.
  Its `99-thinkpixel-bounded.toml` containerd import creates
  `kata-qemu-runtime-rs-ar331-bounded` with the existing runtime-rs artifacts.
- `install-go-canary.py` generates `/opt/kata-ar331/ar-go-bounded/configuration.toml`
  from the shipped Go QEMU configuration, applying ARM64 installation paths,
  `disable_guest_empty_dir=true`, seccomp and annotation restrictions. Its
  `99-thinkpixel-go-bounded.toml` import creates `kata-qemu-ar331-bounded`.
- Both imports live in
  `/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/`. Existing vendor
  handlers and the generated main configuration were not edited.

Both scripts refuse differing existing configuration. They require trusted host
root access and do not restart services. The tested installation used explicit
`systemctl restart k3s-agent` after each configuration change, checked agent health,
then created fresh Pods. A containerd base spec is loaded at startup; restarting
an old container does not replace its saved spec. See the
[pinned containerd documentation](https://github.com/containerd/containerd/blob/v2.3.4/docs/cri/config.md).

The [diagnostic RuntimeClass](../../deploy/kata/bounded-runtimeclass.json) is pinned
to worker02. For the Go comparison, change both its metadata name and handler to
`kata-qemu-ar331-bounded` when generating a separate manifest. The bounded runtime-rs class is now selected for the measured KAS-022 lane.
Do not select the Go comparison or copy selectors to other workers without
qualification. See the closure record for measured overhead and its scope.

## Reproduce bounded probes

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -o /tmp/resourceprobe ./test/security/resourceprobe
```

Create a fresh uniquely named Pod using the hardened shape in
`test/security/kata-network-denial.py:pod`: 900-second deadline, deny-all namespace,
non-root UID/GID 65532, read-only root, no capabilities/token, 512 MiB/1 CPU/128 MiB
limits, and `/tmp` on an `emptyDir` with `sizeLimit: 32Mi`. Select worker02 and the
explicit diagnostic RuntimeClass. For the memory-backed comparison additionally
set `emptyDir.medium: Memory`. Install policy before creating the Pod.

Copy the binary through the trusted operator connection using `kubectl exec -i`
into `/tmp/resourceprobe`, then chmod 500. Run its `pids`, `cpu`, `memory` or `disk`
mode. Use a separate Pod for destructive memory/disk tests. Read trusted CRI resource
settings, guest counters, kubelet volume stats and eventual Pod termination reasons;
do not infer enforcement from the spec or a zero command exit alone. `disk` reports
writes and does not assert a quota passed. `memory` must be killed before completing
640 MiB; an OOM may kill only the exec process before the container status changes.

Retain objects and selected non-sensitive results. Each probe terminates through
its active deadline; shorten that deadline after collecting results to release
capacity. Do not fill the node disk or run an unbounded fork/memory test.

## Maintenance / rollback

Do not mutate artifacts beneath running Kata workloads. Stop scheduling a diagnostic
handler, end its test Pods and confirm their VMs have exited before removing its
operator-owned import and restarting the agent. Retain the configuration and evidence
for review. Removal is not automatic. Existing runc and original Kata handlers are
independent. No worker01/03 configuration changes are part of these diagnostics.
