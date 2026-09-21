# ADR-0024: Agentd authenticated gRPC channel

Status: Accepted 2026-09-21

## Context

ADR-0002 selects an agentd-initiated TLS 1.3 gRPC bidirectional stream. AGD-001
provided the wire schema and AGD-002 the supervisor startup boundary. The channel
must authenticate one Sandbox identity without becoming execution authority or
inventing a permissive replacement for the pending issuer/persistence work.

## Decision

The `grpctransport` adapter provides an outbound client and dedicated server for
`thinkpixel.agentd.v1.AgentTransport.Connect`. Both use TLS 1.3 exclusively, HTTP/2,
explicit trust bundles and exact DNS/URI identities. There is no plaintext,
system-root, proxy-environment or caller-header identity fallback. Server and
client trust roles are separate; a bundle validating the local role's certificate
is rejected. Certificate chains require strong keys, and client leaves have only
clientAuth, one canonical tenant/SandboxBinding/Attempt URI SAN and at most
15-minute lifetime. Server leaves have only serverAuth and one exact DNS SAN.
Wildcards, extra identities, invalid EKU/time and weak keys fail closed.

The neutral transport admission port derives identity from the verified
certificate, never Hello. Its mandatory trusted Authorizer receives that identity,
certificate digest/expiry and proof, then supplies immutable expectations, a
durable connection ID/epoch, finite authority deadline, per-frame Check and Close.
No default implementation admits streams. Current binding, effective provider
state, bootstrap consumption, durable epoch CAS, per-identity admission rate,
revocation and operation replay semantics remain this port's responsibilities.
Callbacks must honor cancellation, be concurrency-safe and avoid raw payload logs.

The channel checks Hello compatibility and binding before Welcome, rejects header
identity on handshake frames, and binds later frames to the accepted version,
connection/epoch and complete binding. Every send and receive invokes the trusted
Check before delivery. Initial streams require a 32-byte bootstrap proof; rotation
frames and reconnect without proof remain disabled until their lifecycle exists.
The per-server active-identity map is a concurrency bound, not durable authority.
It cannot replace cross-replica epoch/fence checks.

Resource bounds are concrete: at most 128 configured accepted connections, one
stream per connection, a connection accept-rate limit equal to configured
capacity per second/burst, 16 KiB headers, 1 MiB wire frames, recursion depth 16,
fixed 64 KiB stream/1 MiB connection flow-control windows, and 64 application
frames per second/burst in each direction. Negotiated payload/frame limits may
narrow the ceilings. No application queue or disk spill is added. Send/receive
and trusted frame checks are deadline-bounded. TLS/Hello setup is at most five
seconds; active streams end by authority/client/server-certificate expiry and
a quiet peer is closed after the negotiated liveness window, even without a
caller blocked in receive. Local writes do not reset that watchdog. Callers honor
context cancellation; structured heartbeats arrive in AGD-008.

Use pinned gRPC Go v1.83.2 and protoc-gen-go-grpc v1.6.2. The generated bindings
join the existing drift check. The dependency is justified by the already accepted
transport, typed streaming, TLS integration and bounded HTTP/2 flow control.

## Consequences

Real TCP/TLS tests exercise identity rejection, bidirectional delivery and failure
closure with ephemeral test certificates. No CA key or test credential is committed.
Transport errors are fixed codes/text and do not return raw TLS/authority errors.

This implements the transport adapter, not its production admission service.
The binary remains dormant pending AGD-004 issuance/bootstrap loading and AGD-005
binding composition. No listener is added to the public AR API, no insecure
standalone default is introduced, and no harness launch is enabled. Durable
reconnect, replay reconciliation and production deployment remain their later gates.

See the [transport runbook](../operations/agentd-transport.md) and
[AGD-003 evidence](../evidence/agd-003-transport.md).
