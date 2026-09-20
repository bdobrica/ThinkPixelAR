# KAS-015 — Kata network denial checks

Date: 2026-09-20. Existing ARM64 homelab, worker `k3spi-02`;
K3s v1.36.4+k3s1, containerd 2.3.4-k3s1.36, Kata 3.31.0 runtime-rs/QEMU.
No new infrastructure or credentials in probe Pods.

## Reproduction

Build `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/networkprobe ./test/security/networkprobe`.
Use `GOARCH=amd64` on an amd64 target. Copy this test binary and
`test/security/kata-network-denial.py` to the operator host with kubectl access.
Run `python3 kata-network-denial.py --probe /tmp/networkprobe --runtime-class
kata-qemu-runtime-rs-ar331 --node k3spi-02` with operator Kubernetes permissions.
The runner creates timestamped test namespaces and retains all resources. Pods
have a 15-minute deadline; no existing policy or application is modified.

The restricted namespace's all-Pod policy is installed before its probe starts.
It permits one exact trusted test peer on TCP 8080. A second Kata probe without
that policy establishes endpoint reachability. Both use the same pinned image,
node, runtime and restricted security settings, with no service-account token.
This fixture exercises network denial, not production AR endpoint authentication.

## Live results

Retained namespaces: `ar-net-{deny,control,targets}-20260920175405`.

| TCP destination | Control Kata Pod | Restricted Kata Pod |
| --- | --- | --- |
| Explicitly allowed peer, 8080 | Connected | Connected |
| Forbidden peer, 8080 | Connected | Denied |
| Kubernetes service, 443 | Connected | Denied |
| Direct control-plane endpoint, 6443 | Connected | Denied |
| Metadata 169.254.169.254, 80 | Unavailable | Denied, with host evidence below |

No metadata server exists in the homelab. Its failed connection alone was **not**
counted as denial. On the trusted worker, `iptables-save -c -t filter` identified
the restricted Pod's CNI REJECT rule by the exact Pod/namespace comment. An isolated
additional metadata probe advanced that same rule's counter from **0 packets /
0 bytes to 1 packet / 60 bytes**, while reporting `connected:false`. The Pod ran
only a sleep plus the explicit probe. This establishes filtering on that path
without requiring a real cloud metadata server or altering host addressing.

CNI resynchronization renamed an earlier chain. The successful before/after
measurement used current chain `KUBE-POD-FW-NSHFEHYHJQHCN5DX`; never assume these
names remain stable or compare counters across different chains. Reproduce the
counter check by matching the test namespace in `iptables-save`, running one
metadata probe with `kubectl exec`, and reading the same current rule again.

## Verification and scope

`go test -race ./test/security/networkprobe/...` and `make verify` passed.
The socket test proves the probe distinguishes a listening endpoint from a
closed endpoint. The live runner rejects unavailable API/peer positive controls
as inconclusive rather than passing them as denial.

This is IPv4 TCP evidence for the tested homelab tuple. It does not qualify an
untested dual-stack CNI, UDP/DNS/rebinding behavior, gateway authentication or
all network modes. Production qualification requirements remain in the
[RC infrastructure guide](../operations/rc-infrastructure.md). The network hook's
trusted qualifier still needs evidence for every guarantee of its selected class;
these test results alone are not a blanket admission grant.
