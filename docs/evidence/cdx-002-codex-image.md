# CDX-002: local Codex runtime image evidence

Date: 2026-09-23. Scope: local demo packaging under
[ADR-0048](../adr/0048-codex-demo-runtime-image.md) and the owner-approved
[license exception](cdx-002-local-demo-license-exception.md).

## Artifact

The combined OCI archive was built and loaded locally as
`thinkpixel-codex:development`. The tag is a convenience, not an immutable
AgentRuntimeSpec reference. No registry publication or cluster deployment occurred.

| Artifact | SHA-256 digest |
| --- | --- |
| OCI index | `184827fafb52373e4aec2002ae3a37c2ad3a7dc69388fb9e744549a73b769739` |
| Linux amd64 manifest | `1025c72c39aed79bb09044ed0f3b8aca1eaed1f086268adda6cd059fad795547` |
| Linux arm64 manifest | `2e784cb2eb430fae7a95587a3d466f525072cbcc0d009892334d2f854bbe19a1` |
| Linux amd64 configuration | `1e391d41f71be0d60a2ad7dcc875b7f5db097f821a0c7d771ad7656d440f54fb` |
| Linux arm64 configuration | `3c2ecdd753e793633bc1f35c0c310c052a07ddf303c00cc7be8da05dd2d79ba0` |

The archive `/tmp/thinkpixelar-cdx002.oci.tar` contains 244,504,064 bytes. All
22 content-addressed blobs were rehashed successfully. The index digest agrees
with BuildKit's metadata; platform manifests/configurations were read from the
archive, not inferred from tags. Configuration digests are not distribution
manifest digests. Both platforms declare UID/GID `65532:65532` and agentd as the
entrypoint. The archive is a local artifact, not committed source or a published
registry reference.

## Inputs and build route

The official Codex `rust-v0.155.0` release archives passed their published SHA-256
checksums before packaging and again inside the extraction stage:

- amd64: `e415cc3adb94ade16e8d44b4dd58a9201cc34b2ee51a5d6eddf2a3a00aecb6c0`;
- arm64: `8b4a9c356916c515f7c93f918a01b8fa1371bcc9758addbfa723b85fbec5694b`.

Large direct downloads/builds stalled and ended with BuildKit `Unavailable`/`EOF`.
The successful build used the [documented local-input route](../../agent-images/codex/README.md):
bounded ranged downloads assembled into checksum-verified upstream archives, and
agentd compiled from the tested working tree based on `0c80c4e` plus this packaging
change. Existing unrelated Go edits were whitespace-only and left untouched.
Go `1.26.7`, `CGO_ENABLED=0`, `GOOS=linux`, per-platform `GOARCH`, `-trimpath`,
`-buildvcs=false`, and `-ldflags='-s -w'` matched the Docker build flags.

Agentd executable SHA-256 values:

- amd64: `f44cbd6ebc5a58b712bbc60895a6645e44af8a29700fc530b3a8ab69fa0344cc`;
- arm64: `2fda8fe187fe0e3ba5a533cd964c34c957beb39fc8e8612e0e989c3eef06cd57`.

Docker 29.0.1 / BuildKit 0.25.2 exported the two platforms with named contexts
`build`, `codex-amd64` and `codex-arm64`, then `docker load` loaded that exact OCI
archive for smoke testing. This qualifies the local-input packaging route. The
default Docker-contained Go compilation route did not finish in this environment.
No publisher-signature, reproducible-build or release-provenance claim is made.

The [generated OS package inventory](cdx-002-image-packages.tsv) contains 35
packages; names, versions, declared licenses, origins and aports commits matched
between both platform images. It was generated from the `P`, `V`, `L`, `o`, `c`
fields in `/usr/share/thinkpixel/apk-installed.txt`, sorted by package name.
Codex LICENSE/NOTICE are checksum-verified and readable by the non-root image user.

## Checks

- `sh scripts/smoke-codex-image.sh`: passed on the loaded amd64 image, with network
  disabled, dropped capabilities and read-only root. Exact CDX-001 executable hash,
  312-file stable schema fingerprint, initialize/initialized, pre-init/duplicate-init
  rejection, empty local thread listing and clean EOF all passed.
- Git initialized a temporary repository; Bash/ripgrep, CA roots and readable Codex
  notices passed. The supervisor smoke passed protected bootstrap handling,
  incomplete-transport rejection, privilege/syscall observations and SIGTERM.
- ARM64 under local emulation: `codex --version` returned `codex-cli 0.155.0`;
  agentd rejected missing bootstrap with exit 1 and a sanitized configuration error.
  The version-only probe reported that temporary-home PATH aliases were not created;
  this did not prevent version execution and does not qualify tool execution.
- A deliberately invalid local archive failed at `/tmp/codex.tar.gz: FAILED`
  before extraction. Named contexts do not skip the archive integrity check.
- The exception expiry rejection passed with a synthetic UTC date of 2026-12-23.
- `make verify`, shell syntax checks and the task diff checks passed.

Advisory review on this date compared installed package-origin versions with the
listed fix thresholds in Alpine's current [main](https://secdb.alpinelinux.org/v3.23/main.json)
and [community](https://secdb.alpinelinux.org/v3.23/community.json) databases, using
the image's `apk version -t`: 190 comparisons completed, with no installed version
below a listed threshold. Database SHA-256 values were
`c4b9783fcdb2f4c2448f2dbc4817bd7fe79558b1f803652a05ec79210a2eb523`
and `46cdff9f81ee61e304bb7bafdbffe84513bafb1d0e901856dc7f1da21dc00cea`.
This is a limited fix-version check, including the database's zero-valued entries;
it does not establish absence of vulnerabilities or cover issues absent from secdb.
The reviewed upstream [Codex advisory GHSA-w5fx-fh39-j5rw](https://github.com/openai/codex/security/advisories/GHSA-w5fx-fh39-j5rw)
lists versions through 0.38.0 as affected and 0.39.0 as patched; the selected
0.155.0 release is outside that affected range. Rust transitive dependencies were
not independently scanned.

## Limits and next work

This closes image construction and scoped local packaging validation. It does not
claim an implemented Codex HarnessAdapter, a model turn, native ARM64 execution,
durable Codex state, generic conformance or Kata isolation. CDX-003 implements
production startup/handshake; thread/turn/events/interrupt and CDX-016 provide the
live demo slice. No model/provider credential or paid infrastructure was used.

The local-demo license exception expires before public distribution or on
2026-12-23. Public release still needs the complete license/source disposition,
OS/Rust vulnerability review, release SBOM/provenance and approved registry
publication. `make verify` scans Go dependencies, not the Rust executable or OS
packages. Optional Codex helpers and task-specific language toolchains are outside
this package's tested surface.
