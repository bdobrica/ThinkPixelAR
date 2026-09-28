# Standalone Session resume

`thinkpixel-session-resume -config /protected/resume.json` reconciles one approved
SES-004 boundary onto replacement Kubernetes compute. It connects to the existing,
migrated AR database through `THINKPIXELAR_DATABASE_URL`; it does not migrate,
create a Session or admit an Execution. Repeating the same command reloads the
operation journal and returns the original result after successful publication.

Build on Linux:

```sh
go build -o thinkpixel-session-resume ./cmd/thinkpixel-session-resume
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -o thinkpixel-resume-probe ./cmd/thinkpixel-resume-probe
```

The pinned ARM64 image must contain the probe at
`/usr/local/bin/thinkpixel-resume-probe` and the pinned Codex binary. The controller
uses the operator's SSH alias and Kubernetes exec; no operator credentials are
projected into the guest. The probe has no listener or turn capability. It restores
and checks the exact thread, reaps its child and removes the fresh process home.
The infrastructure probe temporarily selects the built-in provider without a
model route or credential; its Codex client rejects turns. A subsequent Execution
gets its own supervisor, authorized model route and fresh authority. The candidate
runs the probe binary with `-idle` as PID 1 to reap adopted descendants between
checks. `Dockerfile.codex` packages the probe alongside agentd.

## Protected approval and export

The JSON shape is [`sessionresume.Config`](../../internal/adapters/sessionresume/config.go).
Its fields select one exact `Request` (caller, tenant, Session, checkpoint,
operation ID and expected version), the saved `Binding`, signed `Manifest`,
trusted Ed25519 `PublicKey`, `Issuer` and `KeyID`. Byte slices use JSON base64.
`Version` is 1. `Enabled` and `ExpiresAt` provide current, finite operator approval;
the file is reread before forward work and publication. Only LOCAL authority is
supported. Revoking/changing approval stops new work; identifiers are not authority.

`Directory` contains immutable files named `workspace`, `restore` and `rollout`.
This initial export format maps only to `/workspace/context.txt`,
`/state/restore.json` and `/state/rollout.jsonl`, each bounded to 1 MiB (restore
metadata is further bounded to 4 KiB by the guest). The signed Workspace root is
the SHA-256 of the Workspace file; the signed vendor entries bind the other two
files by size, format and digest. Keep the approval, exported objects and their
parent directory on protected AR-only storage, with files mode 0600 or 0400 and
parent directories mode 0700. Retain them independently of candidate compute.

`SSH`, `Namespace`, `NamespaceUID` and `Image` bind the operator connection,
pre-created namespace and immutable image. The namespace must enforce restricted
Pod Security and contain exactly one deny-all NetworkPolicy. The supported profile
is the exact checked-in `coding-homelab-arm64` digest. Replacement compute uses
`k3spi-02`, `kata-qemu-runtime-rs-ar331-bounded`, its saved CPU/memory limits,
paired local-path PVCs and a **fresh 32 MiB bounded scratch slot**. Prepare capacity
with the existing [scratch procedure](../../docs/operations/bounded-scratch.md).
Retained slots are never silently reused or reformatted.

`ProofScript` selects a protected copy of
[`kata-host-proof.py`](../../test/security/kata-host-proof.py), and
`ProofScriptDigest` pins its SHA-256 with the `sha256:` prefix. The controller runs
it on `pi@10.10.10.12` using `~/.ssh/id_k3spi`, independently of the guest. Readiness
also checks live ownership, effective Pod configuration, exact PVC identities,
bounded scratch backing and restored bytes.

The [live scenario](../../test/e2e/standalone/README.md) produces this approval from
its committed metadata and runs the real executable twice, including restart
replay. It is the executable configuration example; its authentication, checkpoint
publication preparation, model and ordinary Execution dispatch remain fixtures.

## Retry and cleanup

Provider failures retain the same candidate. The command retries bounded calls
for at most 15 minutes; keep the same operation/config after an unavailable result.
Integrity failures abandon the candidate and publish DEGRADED rather than READY.
For an already abandoned candidate, run:

```sh
thinkpixel-session-resume -config /protected/resume.json -cleanup
```

Cleanup is serialized with creation, uses UID-preconditioned foreground deletion,
and requires Sandbox and Pod absence before clearing the attachment reservation.
It never deletes checkpoint exports or Workspace/state PVCs. Uncertain cleanup
stays pending. The operator retains responsibility for retiring test namespaces,
retained PVCs and scratch capacity using their normal storage lifecycle.

This command is the bounded standalone resume entrypoint. General HTTP lifecycle
wiring, arbitrary repository exports, AG/gateway credential integration and
recovery of running or ambiguous work remain separate tasks.
