# AGD-003 — authenticated outbound sandbox transport

Implemented 2026-09-21; [ADR-0024](../adr/0024-agentd-authenticated-grpc-channel.md).

## Scope

The adapter implements real agentd-initiated TLS 1.3/HTTP2 gRPC Connect, generated
stubs, closed handshake/frame validation and mandatory trusted admission/frame
checks. It has no insecure fallback, default authorizer, authority minting or
public API listener. Application/binary composition awaits issuance/bootstrap
and durable binding work in AGD-004/005. The configured supervisor remains dormant.

## Verification

- Focused race tests over real loopback TCP/TLS exercise bidirectional frame
  delivery, exact certificate-to-Hello identity, per-direction trusted checks,
  private callback copies, cleanup and server cancellation.
- Negative tests cover missing/untrusted/expired/overlong/wrong-EKU client
  certificates, wrong trust domain, extra SAN, cross-Sandbox payload claims,
  TLS 1.2, wrong server CA/name, wildcard server identity, shared-role CA and
  missing trusted admission/roots.
- Admission denial, wrong persisted binding, invalid bootstrap proof, missing
  per-frame check, expired authority, live lease expiry, revocation and a second
  active identity stream fail closed. A quiet peer closes even without a caller
  blocked in receive. No rejected frame reaches the handler.
- Closed enums, unknown fields, wrong direction/version/binding/epoch, oversized
  event/diagnostic/wire data, deep wire nesting, missing mutation identity/deadline
  and rate exhaustion are rejected.
- Focused tests and `make verify` passed, including race, generated drift,
  dependency/license inventory, static checks and vulnerability scan.

All test keys/certificates are generated in memory; no live credentials or issuer
private key is committed. This is local adapter qualification, not a homelab
deployment or complete issuer/reconnect/rotation/replay qualification. Extended
malformed and half-open tests remain AGD-014/015; current tests do not claim
application-level replay acknowledgements or cross-replica durable epoch CAS.

## Dependency review

The accepted transport ADR requires gRPC. Added runtime `google.golang.org/grpc
v1.83.2` and its RPC status module
`google.golang.org/genproto/googleapis/rpc
v0.0.0-20260803160001-6ac0973c030d`; both checked-in module licenses are Apache-2.0
and recorded in the repository license inventory. Existing `golang.org/x/time
v0.14.0` is now a direct dependency for bounded connection/frame token buckets.
No module replacement or unpinned version is introduced.

The generated service uses `protoc-gen-go-grpc v1.6.2` with the already selected
Protobuf compiler/runtime. Versions were checked against the
[official release history](https://github.com/grpc/grpc-go/releases).
Fixed window options and TLS integration were reviewed against the
[upstream Go API](https://pkg.go.dev/google.golang.org/grpc@v1.83.2) and the downloaded
pinned module source. These dependencies replace no ThinkPixel authority boundary.

The initial `v1.84.0` selection was rejected by govulncheck for
[GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443). The
[upstream advisory](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj)
identifies `v1.83.2` as a patched stable release, so the final pin uses that
release without a scanner waiver. The advisory concerns xDS servers; this adapter
uses the ordinary gRPC server, and the inspected `v1.84.0` source also contained
the header guard. The scan finding therefore does not establish an exploitable
panic in this adapter. The final repository scan validates the selected pin.

Reproduction: [transport runbook](../operations/agentd-transport.md).
