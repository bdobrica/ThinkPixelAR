# ADR-0048: Package the Codex demo harness with agentd

Status: Accepted 2026-09-23

## Decision

Build a separate Codex runtime image containing the repository's agentd binary,
the exact Codex 0.155.0 musl release selected by ADR-0047, and the basic coding
tools Git, Bash and ripgrep with CA certificates. Use the existing Go builder
pin and a digest-pinned Alpine runtime. Select checksum-verified upstream Codex
archives separately for Linux amd64 and arm64; unsupported architectures fail
the build. Pin direct APK versions and retain the resolved transitive inventory.
The built OCI digest, not a development tag, identifies an executable artifact.
Package-repository retention means future rebuilds are not promised bit-identical.

For local demos with unreliable downloads, allow Docker named contexts containing
only checksum-pinned upstream archives and agentd cross-compiled from the tested
checkout with the same pinned Go version/flags. The extraction stage independently
rechecks archive checksums. Record this build route and binary hashes explicitly;
it does not substitute for release source provenance.

Agentd remains the non-root image entrypoint and accepts work only through its
existing protected bootstrap and authenticated transport. The adapter will launch
`/usr/local/bin/codex` directly over stdio. Packaging neither auto-starts Codex nor
implements the HarnessAdapter. Do not add a harness TCP listener, credentials,
operator configuration or a general language-toolchain collection to this image.

Keep `/workspace` as the Workspace attachment and reserve `/state/codex` for
subsequently qualified durable vendor state. The default home is ephemeral;
packaging does not designate a credential-bearing Codex home as checkpoint-safe.
Kata, network controls and live infrastructure admission remain trusted external
requirements. The image grants no privileges and does not itself prove isolation.

## Qualification and release boundary

Reuse CDX-001's real executable/schema/stdio probe inside the image, exercise its
coding tools, and reuse the existing agentd bootstrap/privilege/shutdown smoke
test. Keep local image evidence distinct from ARM64 hardware execution, model
turns and Kata qualification. The next task is production adapter startup and
handshake (CDX-003), followed by thread/turn/events/interrupt and a live Kata turn.

The owner approved a narrowly scoped local-demo packaging exception for the
GPL tools. It does not authorize public image distribution. Record its expiry,
license/source references and release obligations in the
[exception record](../evidence/cdx-002-local-demo-license-exception.md).
Preserve Codex notices and the OS package inventory. Image scanning, release
SBOM/provenance and redistribution review remain release requirements rather
than implied results of the Go source gate.

See the [image guide](../../agent-images/codex/README.md) for build commands and
the [CDX-002 evidence](../evidence/cdx-002-codex-image.md) for tested artifacts
and limits. No ThinkPixel integration contract or authority boundary changes.
