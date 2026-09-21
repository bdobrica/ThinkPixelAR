# Agentd credential issuance

AGD-004 implements a local client certificate issuer and the trusted application
issuance/renewal service. These are Go components, not a deployed CA endpoint or
an enabled agentd command. The binary stays in `awaiting_transport` until durable
admission and delivery are composed. See [ADR-0025](../adr/0025-agentd-credential-issuance.md).

## Homelab composition

No paid identity service is required. Provision a separate P-256 CA with
`basicConstraints=critical,CA:TRUE`, `keyUsage=critical,keyCertSign` and
`extendedKeyUsage=clientAuth` in the trusted control-plane secret boundary.
Do not reuse the Kubernetes CA, service/server CA or a generic unrestricted CA.
Keep the signing key out of the repository, database, sandbox, images and logs.
The adapter takes parsed certificate DER and a `crypto.Signer`; it does not
silently generate or discover credentials. AR server certificates and server
trust continue to use their separate service issuer.

1. Construct `localissuer.New(caDER, signer, trustDomain)` with the protected signer
   and exact lowercase DNS trust domain. Retain the immutable signer for the
   adapter's lifetime. The adapter serializes signing calls.
2. Implement `sandboxtransport.CredentialAuthority` against authoritative AR
   persistence and current provider/authority observations. Both authorization
   and atomic registration must check the full binding, fences, revocation and
   deadlines. Registration checks the optimistic version and current predecessor
   digest/connection epoch, enforces issuance limits and records metadata only.
3. Construct `agentdidentity.New(issuer, authority)`. Trusted materialization calls
   `Bootstrap` with persisted identity. Deliver its leaf key/certificate and
   32-byte proof only through the protected ephemeral bootstrap projection,
   alongside the separate expected server trust and non-secret configuration.
4. Admission must compare the registered certificate digest, atomically consume
   the bootstrap proof hash once, allocate the connection epoch and schedule
   exact bootstrap Secret deletion. Failed delivery/acquisition/expiry also
   requires cleanup. Deleting a Secret does not revoke a stolen credential.
5. The trusted authenticated stream handler calls `Renew` with its verified Peer
   and accepted connection ID/epoch after validating a rotation request. Deliver
   the returned new key/certificate through that same protected stream. Do not
   accept caller-provided tenant, SAN, deadline or predecessor identity.
6. Destroy each delivery after projection/send completes, including failures.
   Never log or persist the delivery object or protobuf rotation payload.

Steps 2–5 describe the required AGD-005 integration; no production implementation
of those persistence/Secret/stream handlers is supplied by AGD-004. Tests use an
explicit fake authority to test the service's ordering and failure handling.

## Renewal, failure and CA replacement

Bootstrap TTL is at most ten minutes. Session TTL is at most fifteen minutes.
All certificates stop at the earlier authority, Attempt or issuer deadline;
bootstrap also stops at its bootstrap deadline. New keys are generated for every
issuance. A current credential authenticates a renewal request but is not itself
permission to renew. Old connection credentials need their own expiry/revocation
and epoch checks in admission; issuance does not change an existing TLS session.

On denial, expiry, issuer failure or failed registration, no delivery is returned.
An ambiguous registration must be reconciled by its durable state before retry;
the service does not replay signing automatically. Clock errors fail closed;
operators need synchronized clocks rather than extended credential TTLs.

For CA replacement, distribute the old and new **public** client roots to trusted
AR listeners before switching the signing adapter. Renew into the new issuer.
Retain the old root only for its bounded transition window, then fence remaining
old credentials and remove it. Recreate listeners with the new roots: the current
transport snapshots trust at construction. Emergency compromise requires durable
revocation/fencing and closure of active streams as well as root removal.

## Verification

```sh
go test -race ./internal/app/agentdidentity ./internal/adapters/sandboxtransport/...
make verify
```

All test CA/leaf keys are generated in memory. Tests verify actual X.509 chains,
EKU separation, key pairs, identity, TTL and authority bounds, denial, stale and
concurrent renewal, registration failure, best-effort clearing and CA overlap.
No cluster configuration, credential deployment or live issuer has been changed.

## Durable registry checkpoint

[ADR-0026](../adr/0026-durable-agentd-credential-registry.md) adds PostgreSQL
credential registration, one-time bootstrap consumption and connection checks.
Apply migration 19 using the explicit migration command before composing
`postgres.NewAgentdCredentials`. The registry stores only IDs/digests/validity.
It is not an authority adapter: the current provider/Run checks, proof-consumption
cleanup transaction, frame admission and protected delivery remain AGD-005 work.
Do not expose registry methods directly to sandbox requests or treat a registry
lookup as current authority. No running listener is enabled by the migration.
