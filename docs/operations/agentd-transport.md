# Agentd authenticated transport adapter

AGD-003 implements `internal/adapters/sandboxtransport/grpc`: an outbound agentd
client and a dedicated AR gRPC server. The binary/public API do not yet compose
these adapters. [Issuance and renewal](agentd-credentials.md) are implemented in
AGD-004; durable binding, bootstrap loading/delivery and rotation wiring are AGD-005. There is no default authorizer and no harness launch.

## Trusted composition

Construct `ServerConfig` with the AR service certificate/key, exact DNS name,
dedicated client trust bundle, exact trust domain, finite connection capacity
(1–128), an Authorizer and a context-aware handler. `Serve` receives a dedicated
internal listener. Cancel its context to close the listener and active streams.
Do not expose this endpoint as public REST/SSE or terminate client identity into
unsigned proxy headers. Deployment ingress/network policy remains required.

The Authorizer is a trusted implementation of the
[admission port](../../internal/ports/sandboxtransport/admission.go). It must:

1. Resolve persisted current binding by the verified certificate's tenant,
   SandboxBinding and Attempt, and verify the recorded certificate/proof,
   effective provider state, current generation, authority and deadlines.
2. Consume bootstrap and allocate a fresh durable connection identity/epoch under
   the appropriate transaction. Apply per-identity admission rate limits. Handle
   a failed handshake as a failed exchange; no consumed proof becomes reusable.
3. Return immutable expected protocol/build/adapter/binding evidence and a finite
   lease no later than the certificate or authority deadline.
4. Supply concurrency-safe, cancellable Check/Close functions. Check revalidates
   current authority/epoch, sequence/replay/idempotency and registered payload
   semantics before every sent or received frame. Close releases only this epoch.
   Neither function logs raw frames or credential material.

The transport cannot determine these facts from a self-reported Hello. Its local
active-identity map rejects simultaneous streams on one server only; durable
cross-replica arbitration is still mandatory. Tests use an explicit fake trusted
Authorizer, not a production permit-all implementation.

The client takes `ClientConfig` with exact `https://DNS[:port]` endpoint/server
name, separate server roots, its client certificate/key, immutable expected
handshake metadata, bootstrap Hello and a command/frame Check. `Connect` owns one
stream and makes no inbound listener. Its dialer bypasses proxy environment and
uses the operator's DNS. No system roots, insecure flag or automatic application
retry is exposed. A successful return means authenticated compatible connection,
not harness Ready or authorization to execute an external action.

`Session.Send`/`Recv` allow one caller per direction; concurrent calls in the same
direction fail. `Welcome()` returns a copy. Close sessions when no longer used.
The caller's Check remains necessary on both sides even though only the trusted
AR side can authorize authoritative state. Cancellation and deadlines close the
RPC and unblock pending I/O. Handler/admission callbacks are trusted code and
must cooperate with cancellation; the adapter cannot safely terminate arbitrary
Go callbacks that ignore it.

## Security and bounds

- TLS 1.3, HTTP/2, exact server DNS/SNI and one canonical client URI:
  `spiffe://<trust-domain>/tenant/<uuidv7>/sandbox/<uuidv7>/attempt/<uuidv7>`.
- Client leaves are non-CA, clientAuth-only, at most 15 minutes, with no additional
  DNS/IP/email identities. Bootstrap's stricter ten-minute limit belongs to the
  issuer/admission record. Server leaves have serverAuth-only and one exact DNS.
- Verified chains require ECDSA keys of at least 256 bits, Ed25519 or RSA of at
  least 3072 bits. Root trust is explicit and role separation is checked.
- Five-second TLS/Hello setup, 16 KiB headers, one stream per connection, fixed
  64 KiB/1 MiB stream/connection windows. Global connection acceptance has a token
  bucket with rate/burst equal to configured connection capacity.
- At most 1 MiB frames, depth 16 and no unknown v1 fields. Negotiated command/event
  and diagnostic bounds are enforced before delivery. Application frames are
  limited to 64 per second/burst per direction. No unbounded queue/disk buffering.
- Mutation commands require stable operation ID/digest and a live deadline;
  payload registration, replay acknowledgements and durable idempotency belong
  to the application Check. Failure is never silently retried as new work.
- Frames must match the accepted binding/version/epoch and allowed direction.
  Hello/Welcome cannot recur midstream. Rotation remains rejected until AGD-005
  wires the credential service into trusted stream handling.
- Authority and both peer certificate expiries bound stream life. Negotiated
  liveness bounds Send/Recv/Check and an independent watchdog closes quiet peers
  even without a pending receive. Only accepted peer frames reset it. HTTP/2 uses
  30-second keepalive and ten-second
  ping timeout. Structured process heartbeats and wider half-open qualification
  remain AGD-008/015.

Certificate issuance, overlapping root rotation, secret projection/cleanup,
bootstrap consumption, durable epoch persistence and reconnect are not provided
by this adapter. No deployment or live homelab transport qualification is claimed.

## Verification and generation

```sh
go test -race ./internal/adapters/sandboxtransport/... ./internal/ports/sandboxtransport/... ./api/agentd/v1
make agentd-protocol-check
make verify
```

Tests generate ephemeral keys/certificates in memory and use real loopback TCP,
TLS and HTTP/2. No fixture key or credential is stored. `make generate` uses
protoc 3.21.12, protoc-gen-go v1.36.12 and protoc-gen-go-grpc v1.6.2; both generated
Go files are checked for drift.
