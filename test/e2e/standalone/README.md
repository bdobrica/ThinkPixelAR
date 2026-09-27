# Standalone continuation on live Kata Sandboxes

This operator-run E2E-001/E2E-002 integration scenario joins real HTTP Session/Execution
admission, PostgreSQL LocalAuthority, signed checkpoint publication, suspend and
resume coordination to two real Codex processes on separate Kubernetes Agent
Sandboxes. It uses the existing ARM64 `k3spi-02` homelab worker and
`kata-qemu-runtime-rs-ar331` runtime. Both Pods require independent trusted-worker
QEMU/KVM and artifact-pin verification, not just Kubernetes readiness.

The model is a deterministic loopback SSE fixture. Authentication, runtime
qualification, lifecycle worker dispatch, file export, readiness and Kubernetes
exec transport are **test composition**. This does not enable the production
`thinkpixelar` executable's API/worker wiring or authenticated agentd transport.
It is not the standalone MVP gate or an AG/LLMGW/TG demonstration.

The scenario creates a fresh restricted namespace with deny-all networking and
no service-account token, Secrets, host mounts or external model traffic. Each
Sandbox has independent Workspace/state `local-path` PVCs and ephemeral scratch.
`local-path` is not CSI snapshotting, storage quota enforcement, or node-loss
qualification. The approved runtime/profile metadata is explicitly a test fixture;
this run does not qualify that profile's full effective resource/network contract.

## Build and import

From the repository root, use the previously checksum-verified
[Codex image](../../../agent-images/codex/README.md), Go 1.26.7, Docker and operator
SSH access. The guest verifies the pinned ARM64 Codex executable itself.

```sh
export GO="$HOME/.local/go/bin/go"
export GOCACHE=/tmp/thinkpixelar-go-cache
probe_dir=$(mktemp -d /tmp/ar-e2e001-build.XXXXXX)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$GO" test -c \
  -o "$probe_dir/agentd.test" ./internal/app/agentd
cp deploy/agentd/config.example.json "$probe_dir/config.example.json"
docker build --platform linux/arm64 \
  --build-arg CODEX_BASE=thinkpixel-codex:development \
  -f test/e2e/standalone/Dockerfile \
  -t thinkpixel-codex:e2e001-probe "$probe_dir"
docker save -o /tmp/ar-e2e001-image.tar thinkpixel-codex:e2e001-probe
ssh -F "$HOME/.ssh/config" -i "$HOME/.ssh/id_k3spi" pi@10.10.10.12 \
  'sudo k3s ctr images import --platform linux/arm64 --digests -' \
  < /tmp/ar-e2e001-image.tar
```

Use the OCI index digest printed by **your** build. Bind the imported tag to that
immutable reference before running; the test rejects mutable tags.

```sh
export THINKPIXELAR_E2E_IMAGE='docker.io/library/thinkpixel-codex@sha256:REPLACE_WITH_BUILD_DIGEST'
ssh -F "$HOME/.ssh/config" -i "$HOME/.ssh/id_k3spi" pi@10.10.10.12 \
  "sudo k3s ctr images tag docker.io/library/thinkpixel-codex:e2e001-probe $THINKPIXELAR_E2E_IMAGE"
```

## Run

Create a disposable database using the repository's development PostgreSQL
instance, then set its URL. Never point this test at retained application data.
The test inserts tenant-scoped records and expects the existing privileged test
database role for fixture setup. It does not drop databases or run migrations.

```sh
export THINKPIXELAR_TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST:PORT/DISPOSABLE_DB?sslmode=disable'
THINKPIXELAR_DATABASE_URL="$THINKPIXELAR_TEST_DATABASE_URL" "$GO" run ./cmd/migrate up
export THINKPIXELAR_E2E_SSH=k3spi
sh test/e2e/standalone/run.sh
```

The SSH alias resolves the control plane through `~/.ssh/config`. The independent
host proof uses `pi@10.10.10.12`, `~/.ssh/id_k3spi`, and the checked-in read-only
Python script through the worker's `python3`. No operator credential enters the
guest. Ordinary Go test runs skip the live scenario; the runner requires all
configuration and cannot silently skip it.

Require Go PASS, `E2E-001 LIVE PASS` and `E2E-002 LIVE PASS`. The scenario checks:

1. HTTP creates one Session and the first locally authorized Execution.
2. A pinned Codex child supervised by agentd completes a loopback-model turn;
   the process is reaped and its fresh home removed.
3. The controller exports only the small Workspace file, bounded rollout and
   restore metadata into fsynced files outside the guest. Both vendor objects
   are covered by the signed checkpoint; credential canaries are excluded.
4. Production services publish Workspace generation 1 and the signed checkpoint,
   suspend the Session, and reject a new Execution while suspended.
5. After durable release authorization, the exact first Sandbox is deleted and
   both Sandbox and Pod absence are confirmed. The exact old fixture PVCs are
   then deleted and their absence confirmed before replacement starts.
6. The resume coordinator reserves fresh identities. New Sandbox/Pod/PVC UIDs
   are required; only the checkpoint export is copied into the new volumes.
   Exact Codex thread restoration must produce no model request before admission.
7. A second HTTP Execution receives a distinct local grant and generation 2.
   Its Codex turn must include the first turn's conversation, preserve the
   first Execution’s Workspace edit byte-for-byte, and finish with HTTP-visible
   `SUCCEEDED` status. A unique marker must appear in structured assistant history
   in the second model request; a marker in user or tool input does not count.
   Resume replay must return the same result without creating another candidate.
   Persisted Session,
   Workspace, checkpoint and Workspace-generation identities must remain stable;
   Execution generation advances from 1 to 2 with fresh Attempt/Sandbox identities.

The guest's resume-readiness check uses synthetic bootstrap correlation IDs; it
does not create a database Execution or receive a grant. Actual turns are
correlated with the admitted Execution/Attempt. Test-only SQL drives unfinished
materialization and terminalization workers after verified guest results; no
production permission or state-machine implementation is changed.

## Cleanup and limits

The test prints and retains its unique `ar-e2e001-*` namespace for inspection,
including on failure. Pods expire after 30 minutes. After collecting the log,
delete **only the printed test namespace** and drop the disposable database.
Do not use a wildcard or delete pre-existing namespaces. A failed run needs a
new namespace; it does not adopt an ambiguous old candidate.

The first Workspace edit is made by the guest test fixture after the real Codex
turn, not by a Codex tool call. Old PVC deletion removes the original storage
from the restore path; it does not attest physical-media erasure. This deletion
is confined to disposable test resources and is not production Workspace cleanup.

The external export directory and ephemeral signing key are test-lifetime data,
not a retained production checkpoint store. Fresh controller objects reload the
durable operation metadata, but this does not prove full server restart recovery.
General export/retention, automatic candidate cleanup/reconciliation, executable
workers, production authentication and gateway credential revocation remain
their existing tasks. See [E2E-001 evidence](../../../docs/evidence/e2e-001-live-continuation.md) and
[E2E-002 continuity evidence](../../../docs/evidence/e2e-002-continuity.md).
