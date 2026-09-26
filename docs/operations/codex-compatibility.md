# Codex App Server demo compatibility

The selected baseline is **Codex CLI/App Server 0.155.0**, adapter kind
`codex-app-server`. Source pins are in
`internal/adapters/harness/codex/compatibility.go`; decisions are in
[ADR-0047](../adr/0047-codex-app-server-version-pin.md).

| Component | Pin / support boundary |
| --- | --- |
| CLI and bundled App Server | `0.155.0`; exact match only. |
| Tested executable | `x86_64-unknown-linux-musl`, SHA-256 `660e159a49e823ac8e5986cb238f73158ce4b957d40d9292f8de90862644b501`. Observed local executable fingerprint, not an OCI or publisher-signature claim. |
| Stable schema export | 312 JSON files; SHA-256 `5b0fbb54807f53f2286a0aab3428cd6893ef7cdfc0efa0f3151450d70a80ddd6`. Lexically ordered relative path + NUL + original bytes for each `.json` file; no experimental export flag. |
| Transport | Child stdio, newline-delimited JSON requests/responses/notifications. No remote listener. |
| Initialization | `initialize`, `initialized`; `experimentalApi: false`. |
| Next implementation surface | `thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`, `turn/completed`, `item/agentMessage/delta`; schema presence checked, execution not yet qualified. |
| AR compatibility identifier | Exact `0.155.0` for this release/schema snapshot, not an upstream wire SemVer claim. AR adapter-contract/event versions remain separate. |
| ARM64 / OCI / Kata | CDX-002 built amd64/ARM64 OCI artifacts: amd64 packaged protocol/tools/supervisor smoke, ARM64 emulated startup only. Native ARM64/Kata/model-turn evidence remains CDX-016 and the intervening adapter work. |

## Reproduce the scoped check

Use an existing exact executable; do not upgrade the operator's installed CLI.
Set an absolute path, then run:

```sh
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/codex \
  go test -race ./internal/adapters/harness/codex \
  -run '^TestPinnedAppServer$' -count=1 -v
```

The probe checks the executable hash before launch, verifies `--version`, exports
and fingerprints schemas, and exercises the real stdio server. Child environment
and home are isolated from operator configuration, history and credentials. It
does not create a thread, start a turn, use a model/provider credential or modify
a cluster. Temporary schema/config/state files are removed by test cleanup; raw
frames and stderr are not logged. A 30-second overall context bounds the probe.
Default `make verify` skips this opt-in test when the binary variable is absent.

## Upgrade and support policy

Support is for an exact artifact and named test scope. There is no claim that all
0.x releases, all platforms or arbitrary same-version builds are compatible. The
pin is a deliberate demo baseline, not a claim that it is the latest release.
Upgrades are explicit repository changes: obtain the intended upstream artifacts,
review schema differences for consumed methods/fields, update fingerprints and
rerun this probe plus the implemented Codex conformance/integration cases. Record
new OCI/platform digests during packaging. Never silently replace an immutable
Session's runtime or infer vendor-state migration safety from a version number.

Do not enable experimental methods to make an incompatible build pass.
[Supervised startup](../evidence/cdx-003-startup.md) now enforces the pinned
initialization identity; broader version incompatibility handling remains CDX-014.
For `adapter_kind: codex-app-server`, bootstrap must use exactly:

```json
["/usr/local/bin/codex", "app-server", "--listen", "stdio://", "-c", "check_for_update_on_startup=false"]
```

The child receives a fresh ephemeral home and no inherited supervisor environment.
Its handshake is bounded by the command/startup deadline and a ten-second ceiling.
Failed initialization stops the child; successful initialization does not create
a thread or establish AR Session readiness. See [ADR-0050](../adr/0050-codex-supervised-initialization.md).
A missing capability fails explicitly. Resume/fork/state portability require their
own evidence, and the
example `codex/thread-v1` state format is not yet a qualified checkpoint format.
The Go verification gate does not vulnerability-scan this Rust executable. See
[CDX-002 image evidence](../evidence/cdx-002-codex-image.md) for actual artifact
digests, source/notice checks, the local-demo exception and remaining release checks.

The [official OpenAI App Server documentation](https://learn.chatgpt.com/docs/app-server)
describes initialization, stdio and version-specific schema export. This evolving
documentation is guidance; the pinned binary/schema and recorded tests determine
this repository's actual compatibility claim.
