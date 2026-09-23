# ADR-0047: Pin Codex 0.155.0 for the first App Server demo

Status: Accepted 2026-09-23

## Decision

Select Codex CLI/App Server `0.155.0`, using the existing installed Linux amd64
musl build for the first local protocol qualification. Pin the executable digest
and its non-experimental generated schema fingerprint in the Codex adapter package.
Use stdio JSONL with initialize/initialized and experimental API disabled. Do not
add a TCP listener, SDK dependency or model call to qualify this initial pin.

The actual executable passed isolated startup, initialize identity, pre-init and
duplicate-init rejection, empty local thread listing and clean EOF shutdown.
Its schema export contains the required thread/turn/interrupt/message surfaces.
This evidence qualifies that local protocol slice, not a complete HarnessAdapter,
an LLM turn, durable vendor-state format or the ARM64/Kata runtime image.

Use the exact release number as AR's harness compatibility identifier for this
schema snapshot. It is not an upstream protocol SemVer negotiation: the upstream
App Server has versioned method/schema families, and initialization returns build
identity rather than an AR compatibility result. The future adapter must compare
actual identity and enforce its selected schema. No wildcard or automatic upgrade
is allowed. A new CLI patch requires refreshed fingerprints and scoped regression
evidence before selection; immutable Sessions retain their existing runtime pins.

## Demo sequence

CDX-002 packages this release for the existing ARM64 homelab and amd64 PC lane,
recording upstream artifact provenance/licenses and OCI/platform digests. CDX-003
adds supervised production startup/handshake, followed directly by thread creation,
turn/events/interrupt and the Kata turn. The local test does not close these items.
No paid infrastructure or dynamic marketplace resolution is required.

See [compatibility/support policy](../operations/codex-compatibility.md) and
[CDX-001 evidence](../evidence/cdx-001-codex-pin.md).
