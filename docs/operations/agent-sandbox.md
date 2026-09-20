# Agent Sandbox core controller and lifecycle tests

The homelab installation uses the core v1.0.0 release only. Cold acquisition needs
no extension controller, warm pool, router or inbound Sandbox Service. See
[ADR-0006](../adr/0006-agent-sandbox-v1-api-pin.md) and
[ADR-0010](../adr/0010-durable-sandbox-acquisition.md).

## Install

`deploy/agent-sandbox/render.sh` downloads the exact upstream release, verifies
its SHA-256, and applies the checked-in Kustomize overlay. Requires curl, sha256sum
and kubectl with Kustomize support. Rendered output is not hand-edited.

```sh
./deploy/agent-sandbox/render.sh > /tmp/agent-sandbox-install.yaml
kubectl create namespace agent-sandbox-system
kubectl apply --server-side --dry-run=server -f /tmp/agent-sandbox-install.yaml
kubectl apply --server-side -f /tmp/agent-sandbox-install.yaml
kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=180s
```

For an existing namespace, omit `create namespace`. Namespace creation precedes
dry-run because dry-run does not persist it for subsequent objects. The installed
CRD is cluster-scoped. Do not remove a CRD as test cleanup: it owns live resources.

Pins:

- [Upstream core manifest](https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.0/sandbox.yaml):
  SHA-256 `4535d101df688c00ccca565b4b76d2e87d697913182a6dbb7339a811b59e02d7`.
- Controller index: `registry.k8s.io/agent-sandbox/agent-sandbox-controller:v1.0.0@sha256:bdde1a3150bd385f7318c974c1516e880b4f826b6b51a3e7f127c2f8c95b55cd`.
- ARM64 manifest: `sha256:f685cbfb572d38a1d497ad6e019c10e4164e945ff23f887da201c111f0a8d2d6`.

The overlay uses non-root UID/GID 65532, runtime-default seccomp, no capabilities
or privilege escalation, read-only root, 50m CPU/64 MiB requests and 1 CPU/256 MiB
limits. This is the trusted controller, not a sandbox workload. Its upstream
cluster role can manage Pods/PVCs/Services/Sandboxes and coordination leases;
it has no Secret read permission. Its service-account token remains confined to
trusted infrastructure. Workload tokens remain disabled.

The homelab operator source/rendered output is retained at
`/home/pi/thinkpixelar-agent-sandbox-v1/` on the controller. No application database,
worker runtime default, existing namespace policy or external paid service changes.

## Live test

Use an operator-owned loopback API tunnel; no kubeconfig or credential is copied
into the repository or into a workload. For this homelab:

```sh
ssh -o ExitOnForwardFailure=yes -L 18001:127.0.0.1:18001 pi@k3spi \
  'sudo kubectl proxy --address=127.0.0.1 --port=18001'
```

In another shell:

```sh
THINKPIXELAR_TEST_KUBE_API=http://127.0.0.1:18001 \
THINKPIXELAR_TEST_NODE=k3spi-02 \
THINKPIXELAR_TEST_RUNTIME_CLASS=kata-qemu-runtime-rs-ar331 \
make test-kas-lifecycle
```

Stop the tunnel/proxy afterward. The test-only HTTP path accepts loopback only;
production client configuration still requires HTTPS. Local users able to reach
the proxy inherit its operator access, so use it only on a trusted operator host.

The fixture creates a unique restricted namespace, preinstalled deny-all policy,
two local-path PVCs, an empty immutable bootstrap fixture Secret, and bounded
Kata sleep workloads. It explicitly exercises provider release; namespaces,
policies, Secrets and PVCs remain for inspection. There is no broad teardown.
Its own data is empty and no real bootstrap/provider credential is injected.

The live fixture uses test binding storage to isolate provider/controller behavior;
real PostgreSQL replay/fencing is tested separately in KAS-017/018. Fixture template
and network callbacks are not production qualification. No effective verifier is
installed: upstream Ready must still yield AR UNKNOWN/EFFECTIVE_STATE_UNVERIFIED.
Physical qualification is a separate gate, not inferred from lifecycle success.
