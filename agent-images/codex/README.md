# Codex agent image

The root [Dockerfile.codex](../../Dockerfile.codex) packages Codex CLI/App Server
`0.155.0` with the repository's `thinkpixel-agentd`, Git, Bash, ripgrep and CA
certificates. It is a separate artifact from the minimal supervisor image.
The Go compiler and downloaded archives are confined to build stages.

```sh
make codex-image-smoke
```

Run this smoke target on an amd64 Docker engine; the protocol probe does not yet
qualify a native ARM64 image.

The local tag defaults to `thinkpixel-codex:development`; override `CODEX_IMAGE`
to select another local tag. Smoke tests use Docker and the existing Go toolchain;
they make no model calls and require no API key. The protocol probe currently
covers Linux amd64. It checks the exact binary, stable schema fingerprint,
initialization and EOF shutdown inside the hardened image, then verifies coding
tools and reuses the supervisor bootstrap/privilege/SIGTERM smoke test.

For the homelab and PC platforms, export a local OCI archive:

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  --file Dockerfile.codex --output type=oci,dest=/tmp/thinkpixel-codex.oci.tar \
  --metadata-file /tmp/thinkpixel-codex-build.json .
```

Build on native workers or a builder supporting ARM64 emulation. Each architecture
selects its own checksum-verified upstream musl archive; other architectures fail
at build time. Base images use immutable index digests. Direct Alpine packages
have exact versions, with signed APK repository verification and the full resolved
package inventory retained at `/usr/share/thinkpixel/`. Repository package retention
and transitive updates can affect future rebuilds; this is not a bit-reproducibility
claim. Preserve the built OCI artifact and its digest. Registry publication is a
separate action; an eventual AgentRuntimeSpec must use a verified registry manifest
or index digest, never the development tag or Docker image configuration ID.

### Reuse local inputs when downloads are unreliable

Docker's [named contexts](https://docs.docker.com/reference/cli/docker/buildx/build/#build-context)
can replace the download and agentd compilation stages. This is an optional local
demo path, not release provenance. Prepare a directory containing only these inputs:

```text
agentd/out/amd64/thinkpixel-agentd
agentd/out/arm64/thinkpixel-agentd
codex-amd64/codex.tar.gz
codex-arm64/codex.tar.gz
```

Obtain the two archives from the exact release URLs in `Dockerfile.codex`; resume
interrupted transfers if necessary. The extraction stage always verifies their
complete pinned SHA-256 values, including with local contexts. Compile agentd from
the verified checkout using the pinned Go toolchain and the same build flags:

```sh
codex_inputs=/absolute/path/to/inputs
test "$(go env GOVERSION)" = "go$(tr -d '[:space:]' < .go-version)"
for codex_arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$codex_arch" go build \
    -trimpath -buildvcs=false -ldflags='-s -w' \
    -o "$codex_inputs/agentd/out/$codex_arch/thinkpixel-agentd" ./cmd/thinkpixel-agentd
done
docker buildx build --platform linux/amd64,linux/arm64 --file Dockerfile.codex \
  --build-context "build=$codex_inputs/agentd" \
  --build-context "codex-amd64=$codex_inputs/codex-amd64" \
  --build-context "codex-arm64=$codex_inputs/codex-arm64" \
  --tag thinkpixel-codex:development \
  --output type=oci,dest=/tmp/thinkpixel-codex.oci.tar \
  --metadata-file /tmp/thinkpixel-codex-build.json .
docker load --input /tmp/thinkpixel-codex.oci.tar
sh scripts/smoke-codex-image.sh
```

Use only operator-prepared agentd binaries from the current tested source; the
context override does not attest their source or compile them. Record their hashes
and Go version with the resulting image evidence. Never point a context at an
operator home or an unrelated cache. Do not commit these binary inputs.

## Runtime boundary

The image runs as UID/GID `65532`. Its entrypoint is **agentd**, which awaits the
protected bootstrap and authenticated dispatch; Codex does not start automatically.
The future adapter executes `/usr/local/bin/codex app-server --listen stdio://`
directly. Production startup/handshake is CDX-003, followed by thread/turn/events/
interrupt; this image alone does not implement them.

Mount the Workspace at `/workspace`. Supply writable ephemeral `/tmp` and a fresh
execution-local home. `/state/codex` is only a mount location for subsequently
qualified vendor state; the image does not set `CODEX_HOME` to durable storage or
declare credential-bearing home content safe to checkpoint. Bootstrap credentials
remain under the protected agentd projection. No operator configuration, credential,
host home directory, Kubernetes client, compiler or model key is copied into this
image. Task-specific language toolchains can be added later when a demo needs one.

The image adds no privileges, host mounts or sandbox exemptions. Kata and trusted
infrastructure still enforce the selected Runtime Profile. Optional upstream voice,
code-mode and nested sandbox helpers are not bundled or qualified; the first turn's
tool-execution policy must be selected explicitly by the adapter under existing
infrastructure enforcement. A container smoke test is not Kata isolation evidence.

## Artifact sources and licenses

Codex archives come from the official
[0.155.0 release](https://github.com/openai/codex/releases/tag/rust-v0.155.0).
Their SHA-256 pins are the release API's published asset digests. The build checks
them before extraction; this is integrity checking, not Sigstore verification.
The release's Apache-2.0 LICENSE and NOTICE are separately checksum-verified and
installed under `/usr/share/licenses/codex/`.

Alpine packages come from its signed `v3.23/main` and `v3.23/community`
repositories. The image's APK database records package versions, origins,
checksums and licenses, including transitive libraries. Git provides repository
operations, Bash shell execution, ripgrep search, and ca-certificates TLS roots;
agentd supplies none of these tools. Corresponding package source and build recipes
are published in [Alpine aports](https://gitlab.alpinelinux.org/alpine/aports).
Git/Bash/BusyBox are permitted as separately packaged tools under
[ADR-0049](../../docs/adr/0049-use-based-dependency-licensing.md), without a
local-demo exception or expiry. Before distributing images to external recipients,
fulfill the source/notice obligations in the
[dependency policy](../../docs/security/dependency-policy.md).
Source `make verify` scans Go dependencies, not
the packaged Rust binary or OS packages; release SBOM, image scanning and provenance
qualification are separate requirements.
