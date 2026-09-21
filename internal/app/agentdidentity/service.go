// Package agentdidentity coordinates trusted credential issuance and renewal.
// It never accepts an identity from a sandbox payload or stores plaintext secrets.
package agentdidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrCredential = errors.New("agentd credential request rejected")

type Service struct {
	issuer    transport.CertificateIssuer
	authority transport.CredentialAuthority
}

func New(issuer transport.CertificateIssuer, authority transport.CredentialAuthority) (*Service, error) {
	if issuer == nil || authority == nil {
		return nil, ErrCredential
	}
	return &Service{issuer: issuer, authority: authority}, nil
}

// Delivery is restricted, short-lived, and explicitly destroyed by its consumer.
// Do not serialize this object into logs, events, artifacts, or durable storage.
type Delivery struct {
	Certificate transport.IssuedCertificate
	Proof       []byte
	Record      transport.CredentialRecord
}

func (d Delivery) String() string   { return "[restricted agentd credential]" }
func (d Delivery) GoString() string { return d.String() }
func (d *Delivery) Destroy()        { d.Certificate.Destroy(); clear(d.Proof); *d = Delivery{} }

func (s *Service) Bootstrap(ctx context.Context, id transport.Identity) (Delivery, error) {
	return s.issue(ctx, transport.CredentialRequest{Identity: id}, true)
}

// Renew must be called only with the TLS peer and accepted connection, not a
// Rotation payload. The authority checks this predecessor is still current.
func (s *Service) Renew(ctx context.Context, peer transport.Peer, connection primitives.ID, epoch uint64) (Delivery, error) {
	if _, err := primitives.ParseID(string(connection)); err != nil || epoch == 0 || !peer.ExpiresAt.After(time.Now()) || !validDigest(peer.CertificateDigest) {
		return Delivery{}, ErrCredential
	}
	return s.issue(ctx, transport.CredentialRequest{Identity: peer.Identity, Peer: peer, ConnectionID: connection, Epoch: epoch}, false)
}
func (s *Service) issue(ctx context.Context, request transport.CredentialRequest, bootstrap bool) (Delivery, error) {
	fail := Delivery{}
	for _, id := range []primitives.ID{request.Identity.TenantID, request.Identity.SandboxID, request.Identity.AttemptID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return fail, ErrCredential
		}
	}
	if ctx.Err() != nil {
		return fail, ErrCredential
	}
	grant, err := s.authority.AuthorizeCredential(ctx, request)
	now := time.Now().UTC()
	if err != nil || ctx.Err() != nil || grant.Identity != request.Identity || grant.Version == 0 || !grant.AuthorityDeadline.After(now) || !grant.AttemptDeadline.After(now) {
		return fail, ErrCredential
	}
	ttl := 15 * time.Minute
	if bootstrap {
		ttl = 10 * time.Minute
		if !grant.BootstrapDeadline.After(now) {
			return fail, ErrCredential
		}
	}
	// Whole-second validity avoids subsecond certificate rounding widening a grant.
	start := now.Truncate(time.Second)
	end := start.Add(ttl)
	for _, deadline := range []time.Time{grant.AuthorityDeadline, grant.AttemptDeadline} {
		if deadline.Before(end) {
			end = deadline
		}
	}
	if bootstrap && grant.BootstrapDeadline.Before(end) {
		end = grant.BootstrapDeadline
	}
	end = end.Truncate(time.Second)
	if !end.After(now) {
		return fail, ErrCredential
	}
	d := Delivery{}
	success := false
	defer func() {
		if !success {
			d.Destroy()
		}
	}()
	d.Certificate, err = s.issuer.Issue(ctx, transport.CertificateRequest{Identity: grant.Identity, NotBefore: start, ExpiresAt: end})
	if err != nil || ctx.Err() != nil || d.Certificate.NotBefore.Before(start) || d.Certificate.NotBefore.After(time.Now()) || d.Certificate.ExpiresAt.After(end) || !d.Certificate.ExpiresAt.After(time.Now()) || !validDigest(d.Certificate.CertificateDigest) || !validDigest(d.Certificate.IssuerDigest) || len(d.Certificate.CertificatePEM) == 0 || len(d.Certificate.CertificatePEM) > 16<<10 || len(d.Certificate.PrivateKeyPEM) == 0 || len(d.Certificate.PrivateKeyPEM) > 4<<10 {
		return fail, ErrCredential
	}
	id, err := primitives.NewID(now)
	if err != nil {
		return fail, ErrCredential
	}
	d.Record = transport.CredentialRecord{Identity: grant.Identity, CredentialID: id, CertificateDigest: d.Certificate.CertificateDigest, IssuerDigest: d.Certificate.IssuerDigest, NotBefore: d.Certificate.NotBefore, ExpiresAt: d.Certificate.ExpiresAt, Bootstrap: bootstrap}
	if bootstrap {
		d.Proof = make([]byte, 32)
		if _, err := rand.Read(d.Proof); err != nil {
			return fail, ErrCredential
		}
		hash := sha256.Sum256(d.Proof)
		d.Record.ProofDigest = "sha256:" + hex.EncodeToString(hash[:])
	}
	// No credential escapes before durable registration succeeds. An ambiguous
	// commit is never retried here; discard delivery and reconcile registration.
	if !bootstrap && !request.Peer.ExpiresAt.After(time.Now()) {
		return fail, ErrCredential
	}
	if s.authority.CommitCredential(ctx, request, grant, d.Record) != nil || ctx.Err() != nil || !d.Record.ExpiresAt.After(time.Now()) || !bootstrap && !request.Peer.ExpiresAt.After(time.Now()) {
		return fail, ErrCredential
	}
	success = true
	return d, nil
}
func validDigest(s string) bool {
	if len(s) != 71 || s[:7] != "sha256:" {
		return false
	}
	b, err := hex.DecodeString(s[7:])
	return err == nil && hex.EncodeToString(b) == s[7:]
}
