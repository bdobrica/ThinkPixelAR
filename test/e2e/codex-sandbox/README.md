# Native Codex in a Kata Agent Sandbox

This operator-run CDX-016 probe executes the real pinned Codex driver on the
existing Kata cluster, with agentd supervisor startup/turn/replay/reap checks and
adapter message/usage/completion/failure/interrupt checks. Model responses come
from a deterministic loopback SSE fixture inside the guest. No API key, provider
call, LLMGW/TG route, AG grant or application-level Session is involved.

The test image contains current agentd and compiled test binaries. Its entrypoint
is a qualification script, **not** the production authenticated agentd host.
Supervisor tests invoke production process control with synthetic trusted inputs;
the complete streamed turn runs directly through the Codex driver. This does not
qualify authenticated control-plane event delivery or HarnessAdapter conformance.

## Build locally

Start with the checksum-verified image from the
[Codex image guide](../../../agent-images/codex/README.md). Verify/record its OCI
digest before building. The CDX-016 evidence uses CDX-002's local image as its
base, replacing the old agentd binary with current source. No registry push is
needed. Run from the repository root with Go 1.26.7 and Docker:

```sh
export GO="$HOME/.local/go/bin/go"
export GOCACHE=/tmp/thinkpixelar-go-cache
probe_dir=$(mktemp -d /tmp/cdx016-build.XXXXXX)
export CGO_ENABLED=0 GOOS=linux GOARCH=arm64
"$GO" test -c -o "$probe_dir/codex.test" ./internal/adapters/harness/codex
"$GO" test -c -o "$probe_dir/agentd.test" ./internal/app/agentd
"$GO" build -trimpath -buildvcs=false -ldflags='-s -w' \
  -o "$probe_dir/thinkpixel-agentd" ./cmd/thinkpixel-agentd
cp test/e2e/codex-sandbox/probe.sh "$probe_dir/probe.sh"
docker build --platform linux/arm64 \
  --build-arg CODEX_BASE=thinkpixel-codex:development \
  -f test/e2e/codex-sandbox/Dockerfile \
  -t thinkpixel-codex:cdx016-probe "$probe_dir"
docker save -o /tmp/cdx016-probe.tar thinkpixel-codex:cdx016-probe
sha256sum "$probe_dir"/*
```

Record the exported OCI index/manifest digest from the build output. The probe
checks the architecture-specific Codex executable hash and stable schema hash;
it rejects unsupported architectures. For amd64 change both Go/Docker platforms
and select a separately qualified amd64 Kata worker/runtime; only ARM64 was live
exercised for CDX-016.

## Run on the existing homelab

Inspect node health/capacity and the pinned controller/runtime first. Use a fresh
namespace. Substitute the **newly built** OCI index digest below; the renderer
rejects mutable image tags. The selected worker must have the image imported
because `imagePullPolicy: Never` prevents any registry fetch.

```sh
ssh -F "$HOME/.ssh/config" -i "$HOME/.ssh/id_k3spi" pi@10.10.10.12 \
  'sudo k3s ctr images import --platform linux/arm64 --digests -' < /tmp/cdx016-probe.tar
export PROBE_IMAGE='docker.io/library/thinkpixel-codex@sha256:REPLACE_WITH_BUILD_DIGEST'
ssh -F "$HOME/.ssh/config" -i "$HOME/.ssh/id_k3spi" pi@10.10.10.12 \
  "sudo k3s ctr images tag docker.io/library/thinkpixel-codex:cdx016-probe $PROBE_IMAGE"
export PROBE_NAMESPACE="ar-cdx016-$(date -u +%Y%m%d%H%M%S)"
for part in infrastructure sandbox; do
  "$HOME/.venvs/py-dev/bin/python" test/e2e/codex-sandbox/render.py \
    --image "$PROBE_IMAGE" --namespace "$PROBE_NAMESPACE" --node k3spi-02 \
    --part "$part" > "/tmp/cdx016-$part.json"
done
ssh -F "$HOME/.ssh/config" k3spi 'sudo kubectl create -f -' < /tmp/cdx016-infrastructure.json
ssh -F "$HOME/.ssh/config" k3spi 'sudo kubectl create --dry-run=server -f -' < /tmp/cdx016-sandbox.json
ssh -F "$HOME/.ssh/config" k3spi 'sudo kubectl create -f -' < /tmp/cdx016-sandbox.json
ssh -F "$HOME/.ssh/config" k3spi "sudo kubectl -n $PROBE_NAMESPACE get pods -o wide"
ssh -F "$HOME/.ssh/config" k3spi "sudo kubectl -n $PROBE_NAMESPACE logs codex-probe"
```

Stop on any failed command; create the workload only after the restricted
namespace and deny-all policy succeed. No Service, Secret, service-account token,
host mount, host namespace or external egress is configured. `/tmp` and
`/workspace` use memory-backed volumes; this is an explicit small qualification
fixture, not admission or resource qualification of the secure coding profile.

Require all six top-level Go tests to pass and the `CDX-016 PASS` marker; Kubernetes
Ready alone is insufficient. Capture logs and the actual Pod owner/UID, runtime,
node and image identities. During the ten-minute post-test hold, run
[the read-only host proof](../../security/kata-host-proof.py) on the worker using
that exact Pod UID and handler, as described in the
[Kata runbook](../../../docs/operations/kata.md#live-host-correlation).
A matching QEMU process and KVM descriptor are required for hardware evidence.

The Pod has a 900-second deadline and the Sandbox expires twenty minutes after
rendering. Capture evidence before expiry: the controller deletes the backing
Pod and retains the Sandbox, namespace and policy. No cluster infrastructure or
unrelated workloads are deleted. Fresh attempts need fresh namespaces. No
production readiness, durable continuation or live governed model claim follows
from this test.
