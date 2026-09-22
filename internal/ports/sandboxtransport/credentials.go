package sandboxtransport

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// CredentialRequest is constructed by trusted materialization or the accepted
// stream handler, never decoded from a sandbox-selected identity/epoch.
type CredentialRequest struct {
	Identity Identity
	// Recovery is trusted control-plane intent, never a sandbox request.
	Recovery bool
	// Renewal requires the current authenticated peer and accepted connection.
	Peer         Peer
	ConnectionID primitives.ID
	Epoch        uint64
}

// CredentialGrant contains current, finite authority read from durable state.
// Version is an opaque optimistic concurrency fence for CommitCredential.
type CredentialGrant struct {
	Identity                                              Identity
	Version                                               uint64
	AuthorityDeadline, AttemptDeadline, BootstrapDeadline time.Time
}

// CredentialRecord contains only non-secret registration metadata. A bootstrap
// proof is stored as SHA-256, never as plaintext. Renewals have no bootstrap proof.
type CredentialRecord struct {
	Identity                                     Identity
	CredentialID                                 primitives.ID
	CertificateDigest, IssuerDigest, ProofDigest string
	NotBefore, ExpiresAt                         time.Time
	Bootstrap                                    bool
}

// CredentialAuthority MUST revalidate binding, provider, current authority,
// revocation, predecessor credential and connection epoch at both calls.
// CommitCredential atomically checks grant.Version and registers the new digest;
// concurrent/stale renewals must fail. It must reject any widened deadline,
// enforce issuance rate limits, and retain predecessor expiry/revocation metadata.
// Bootstrap consumption and exact resource cleanup belong to stream admission.
// Implementations honor context cancellation. There is no permissive default.
type CredentialAuthority interface {
	AuthorizeCredential(context.Context, CredentialRequest) (CredentialGrant, error)
	CommitCredential(context.Context, CredentialRequest, CredentialGrant, CredentialRecord) error
}

type CertificateRequest struct {
	Identity             Identity
	NotBefore, ExpiresAt time.Time
}

// IssuedCertificate is Restricted ephemeral delivery material, never evidence.
type IssuedCertificate struct {
	CertificatePEM, PrivateKeyPEM   []byte
	CertificateDigest, IssuerDigest string
	NotBefore, ExpiresAt            time.Time
}

func (c IssuedCertificate) String() string   { return "[restricted agentd certificate]" }
func (c IssuedCertificate) GoString() string { return c.String() }
func (c *IssuedCertificate) Destroy() {
	clear(c.PrivateKeyPEM)
	clear(c.CertificatePEM)
	*c = IssuedCertificate{}
}

// CertificateIssuer keeps its long-lived signer outside sandbox/database state.
// It issues clientAuth only, an exact identity SAN, and a fresh private key.
// The actual validity may narrow, but never widen, the requested interval.
type CertificateIssuer interface {
	Issue(context.Context, CertificateRequest) (IssuedCertificate, error)
}
