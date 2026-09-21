# ADR-0025: Agentd credential issuance and renewal

Status: Accepted 2026-09-21

## Context

ADR-0002 requires one-time bootstrap proof, short-lived client certificates and
fresh authority validation on renewal. ADR-0024 supplies a transport adapter but
deliberately has no default admission service. Certificate generation must not
become an alternative authority source or require a paid identity service.

## Decision

`agentdidentity.Service` coordinates bootstrap issuance and renewal through
`CertificateIssuer` and mandatory `CredentialAuthority` ports. Trusted
materialization supplies bootstrap identity. Renewal receives only the verified
TLS peer and accepted connection ID/epoch from trusted stream handling; no CSR,
requested SAN, TTL, or identity is accepted from a Rotation payload.

Authorization reads the current durable binding and finite authority/Attempt
deadlines. Registration must atomically revalidate those facts, the predecessor
credential, revocation, connection epoch, issuance rate and optimistic version
after signing. No secret delivery is returned before registration succeeds.
Failed or ambiguous registration discards delivery without automatic retry.
There is no in-memory production authority or permit-all implementation.

Bootstrap credentials last at most ten minutes and include a fresh 256-bit proof
and UUIDv7 credential ID. Registration stores only the proof's SHA-256 digest.
Renewal generates a new key/certificate with at most fifteen-minute lifetime and
no bootstrap proof. Authority, Attempt and issuer expiry bound both; bootstrap
deadline additionally bounds bootstrap. Renewal may outlive its predecessor but
cannot extend Execution authority. Subsecond expiration is rounded down.

The initial local issuer uses an operator-provisioned P-256 signing CA constrained
to clientAuth. It rejects unrestricted/server-role CAs, mismatched keys and invalid
trust domains. Leaves use fresh P-256 keys, random positive serials, digital
signature usage, clientAuth only and one canonical tenant/Sandbox/Attempt URI SAN.
They contain no user names, workload content or other identities. The issuer has
no network, filesystem or environment discovery and generates no CA implicitly.
An external issuer can replace it through the same port.

CA signing keys remain in the trusted control-plane secret boundary. Only bounded
leaf key/certificate delivery and bootstrap proof may enter the sandbox's
ephemeral bootstrap projection or authenticated rotation channel. Registration
contains digests, IDs and validity metadata, never private keys or plaintext
proofs. Delivery has redacted standard Go formatting and explicit best-effort
buffer clearing; this does not promise erasure of every Go/TLS/serialization copy.

Issuer replacement supports overlap by provisioning both public trust roots,
switching the issuer adapter, and removing the old root after outstanding
credentials expire or are fenced. Trust distribution and stream replacement are
operator/application composition responsibilities, not a hidden issuer network
service. No new infrastructure or runtime dependency is required.

## Consequences

AGD-004 provides the issuance/renewal mechanism. AGD-005 still must implement
durable credential/admission composition, bootstrap one-time consumption,
Secret projection/cleanup and the authenticated rotation handler. The existing
transport continues rejecting Rotation until that handler can safely consume it;
the binary remains dormant. This record does not claim deployed credential
delivery, revocation enforcement by TLS alone, or restart/reconnect qualification.

Local tests verify issued chains/key pairs, scope, ceilings, deadline narrowing,
renewal with new keys, denial/revocation, failed registration, concurrent renewal
fencing through the port, and overlapping/removed CA trust. Durable database
concurrency and live Secret cleanup require AGD-005 integration evidence.

See [credential operations](../operations/agentd-credentials.md) and
[AGD-004 evidence](../evidence/agd-004-credentials.md).
