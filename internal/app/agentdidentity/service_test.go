package agentdidentity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Only tests use an in-memory authority. Production must compose durable fences.
type authority struct {
	mu                 sync.Mutex
	grant              transport.CredentialGrant
	records            []transport.CredentialRecord
	deny, rejectCommit bool
}

func (a *authority) AuthorizeCredential(ctx context.Context, r transport.CredentialRequest) (transport.CredentialGrant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deny || ctx.Err() != nil || r.Identity != a.grant.Identity {
		return transport.CredentialGrant{}, errors.New("restricted authority detail")
	}
	if r.Epoch != 0 && (r.Epoch != 1 || len(a.records) == 0 || a.records[len(a.records)-1].CertificateDigest != r.Peer.CertificateDigest) {
		return transport.CredentialGrant{}, ErrCredential
	}
	return a.grant, nil
}
func (a *authority) CommitCredential(ctx context.Context, r transport.CredentialRequest, g transport.CredentialGrant, c transport.CredentialRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deny || a.rejectCommit || ctx.Err() != nil || g.Version != a.grant.Version {
		return errors.New("restricted commit detail")
	}
	a.records = append(a.records, c)
	a.grant.Version++
	return nil
}
func fixture(t *testing.T) (*Service, *authority, *x509.Certificate) {
	t.Helper()
	issuer, ca := makeIssuer(t, time.Now().Add(time.Hour), x509.ExtKeyUsageClientAuth)
	newID := func() primitives.ID {
		id, err := primitives.NewID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := &authority{grant: transport.CredentialGrant{Identity: transport.Identity{TenantID: newID(), SandboxID: newID(), AttemptID: newID()}, Version: 1, AuthorityDeadline: time.Now().Add(time.Hour), AttemptDeadline: time.Now().Add(time.Hour), BootstrapDeadline: time.Now().Add(time.Hour)}}
	s, err := New(issuer, a)
	if err != nil {
		t.Fatal(err)
	}
	return s, a, ca
}
func makeIssuer(t *testing.T, expiry time.Time, eku x509.ExtKeyUsage) (*localissuer.Issuer, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{eku}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry}
	der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	i, err := localissuer.New(der, key, "ar.test")
	if err != nil {
		t.Fatal(err)
	}
	return i, ca
}
func leaf(t *testing.T, d Delivery, ca *x509.Certificate) *x509.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair(d.Certificate.CertificatePEM, d.Certificate.PrivateKeyPEM)
	if err != nil {
		t.Fatal("issued key/certificate mismatch")
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := c.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("client credential usable as server")
	}
	id := d.Record.Identity
	want := "spiffe://ar.test/tenant/" + string(id.TenantID) + "/sandbox/" + string(id.SandboxID) + "/attempt/" + string(id.AttemptID)
	if len(c.URIs) != 1 || c.URIs[0].String() != want || c.IsCA || len(c.DNSNames) != 0 || len(c.IPAddresses) != 0 || c.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Fatal("wrong certificate identity/profile")
	}
	hash := sha256.Sum256(c.Raw)
	if d.Record.CertificateDigest != "sha256:"+hex.EncodeToString(hash[:]) || !d.Record.ExpiresAt.Equal(c.NotAfter) {
		t.Fatal("registration disagrees with certificate")
	}
	return c
}
func TestBootstrapAndFreshKeyRenewal(t *testing.T) {
	s, a, ca := fixture(t)
	d, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Destroy()
	c := leaf(t, d, ca)
	if c.NotAfter.Sub(c.NotBefore) > 10*time.Minute || len(d.Proof) != 32 || !d.Record.Bootstrap {
		t.Fatal("bootstrap bounds")
	}
	h := sha256.Sum256(d.Proof)
	if d.Record.ProofDigest != "sha256:"+hex.EncodeToString(h[:]) {
		t.Fatal("bootstrap hash")
	}
	peer := transport.Peer{Identity: d.Record.Identity, CertificateDigest: d.Record.CertificateDigest, ExpiresAt: d.Record.ExpiresAt}
	connection, _ := primitives.NewID(time.Now())
	next, err := s.Renew(context.Background(), peer, connection, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Destroy()
	nc := leaf(t, next, ca)
	if nc.NotAfter.Sub(nc.NotBefore) > 15*time.Minute || next.Record.Bootstrap || len(next.Proof) != 0 || next.Record.ProofDigest != "" || bytes.Equal(c.RawSubjectPublicKeyInfo, nc.RawSubjectPublicKeyInfo) || next.Record.CredentialID == d.Record.CredentialID {
		t.Fatal("rotation reused bootstrap/key or widened TTL")
	}
	if _, err := s.Renew(context.Background(), peer, connection, 1); err != ErrCredential {
		t.Fatal("superseded predecessor accepted")
	}
	if len(a.records) != 2 {
		t.Fatal("wrong durable registrations")
	}
}
func TestFiniteDeadlines(t *testing.T) {
	for _, which := range []string{"authority", "attempt", "bootstrap", "issuer"} {
		t.Run(which, func(t *testing.T) {
			s, a, ca := fixture(t)
			bound := time.Now().Add(2 * time.Minute).Truncate(time.Second)
			switch which {
			case "authority":
				a.grant.AuthorityDeadline = bound
			case "attempt":
				a.grant.AttemptDeadline = bound
			case "bootstrap":
				a.grant.BootstrapDeadline = bound
			case "issuer":
				s.issuer, ca = makeIssuer(t, bound, x509.ExtKeyUsageClientAuth)
			}
			d, err := s.Bootstrap(context.Background(), a.grant.Identity)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Destroy()
			c := leaf(t, d, ca)
			if !c.NotAfter.Equal(bound) {
				t.Fatal("deadline not narrowed")
			}
		})
	}
}
func TestFailClosedAndSanitized(t *testing.T) {
	for _, which := range []string{"denied", "commit", "expired", "wrong-binding", "zero-version", "cancelled", "invalid-id", "expired-issuer"} {
		t.Run(which, func(t *testing.T) {
			s, a, _ := fixture(t)
			id := a.grant.Identity
			ctx := context.Background()
			switch which {
			case "denied":
				a.deny = true
			case "commit":
				a.rejectCommit = true
			case "expired":
				a.grant.AuthorityDeadline = time.Now()
			case "wrong-binding":
				a.grant.Identity.AttemptID = a.grant.Identity.TenantID
			case "zero-version":
				a.grant.Version = 0
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "invalid-id":
				id.TenantID = "invalid"
			case "expired-issuer":
				s.issuer, _ = makeIssuer(t, time.Now().Add(-time.Minute), x509.ExtKeyUsageClientAuth)
			}
			d, err := s.Bootstrap(ctx, id)
			if err != ErrCredential || len(d.Certificate.PrivateKeyPEM) != 0 || len(d.Proof) != 0 || len(a.records) != 0 {
				t.Fatal("failure leaked delivery or registration")
			}
		})
	}
}
func TestRenewalGuards(t *testing.T) {
	s, a, _ := fixture(t)
	d, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Destroy()
	connection, _ := primitives.NewID(time.Now())
	for _, which := range []string{"expired", "digest", "epoch", "connection", "revoked"} {
		t.Run(which, func(t *testing.T) {
			p := transport.Peer{Identity: d.Record.Identity, CertificateDigest: d.Record.CertificateDigest, ExpiresAt: d.Record.ExpiresAt}
			epoch := uint64(1)
			conn := connection
			switch which {
			case "expired":
				p.ExpiresAt = time.Now()
			case "digest":
				p.CertificateDigest = "invalid"
			case "epoch":
				epoch = 2
			case "connection":
				conn = "invalid"
			case "revoked":
				a.deny = true
			}
			if _, err := s.Renew(context.Background(), p, conn, epoch); err != ErrCredential {
				t.Fatal("invalid renewal accepted")
			}
		})
	}
}
func TestDeliveryDestroyAndFormatting(t *testing.T) {
	s, a, _ := fixture(t)
	d, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, d) != "[restricted agentd credential]" || fmt.Sprintf(format, d.Certificate) != "[restricted agentd certificate]" {
			t.Fatal("formatting exposed restricted material")
		}
	}
	key, proof := d.Certificate.PrivateKeyPEM, d.Proof
	d.Destroy()
	if len(d.Proof) != 0 || len(d.Certificate.PrivateKeyPEM) != 0 || !bytes.Equal(key, make([]byte, len(key))) || !bytes.Equal(proof, make([]byte, len(proof))) {
		t.Fatal("delivery not cleared")
	}
}
func TestMandatoryDependencies(t *testing.T) {
	s, a, _ := fixture(t)
	if _, err := New(nil, a); err == nil {
		t.Fatal("nil issuer")
	}
	if _, err := New(s.issuer, nil); err == nil {
		t.Fatal("nil authority")
	}
}

type issuerFunc func(context.Context, transport.CertificateRequest) (transport.IssuedCertificate, error)

func (f issuerFunc) Issue(ctx context.Context, r transport.CertificateRequest) (transport.IssuedCertificate, error) {
	return f(ctx, r)
}

func TestAuthorityRevokedDuringSigningDiscardsKey(t *testing.T) {
	s, a, _ := fixture(t)
	real := s.issuer
	var private []byte
	s.issuer = issuerFunc(func(ctx context.Context, r transport.CertificateRequest) (transport.IssuedCertificate, error) {
		c, err := real.Issue(ctx, r)
		private = c.PrivateKeyPEM
		a.mu.Lock()
		a.deny = true
		a.mu.Unlock()
		return c, err
	})
	d, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != ErrCredential || len(d.Proof) != 0 || len(a.records) != 0 || len(private) == 0 || !bytes.Equal(private, make([]byte, len(private))) {
		t.Fatal("revoked issuance exposed or retained key")
	}
}

func TestConcurrentRenewalsRequireAtomicRegistration(t *testing.T) {
	s, a, _ := fixture(t)
	d, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Destroy()
	real := s.issuer
	var ready sync.WaitGroup
	ready.Add(2)
	s.issuer = issuerFunc(func(ctx context.Context, r transport.CertificateRequest) (transport.IssuedCertificate, error) {
		ready.Done()
		ready.Wait()
		return real.Issue(ctx, r)
	})
	peer := transport.Peer{Identity: d.Record.Identity, CertificateDigest: d.Record.CertificateDigest, ExpiresAt: d.Record.ExpiresAt}
	connection, _ := primitives.NewID(time.Now())
	results := make(chan error, 2)
	for range 2 {
		go func() {
			next, err := s.Renew(context.Background(), peer, connection, 1)
			defer next.Destroy()
			results <- err
		}()
	}
	success := 0
	for range 2 {
		if <-results == nil {
			success++
		}
	}
	if success != 1 || len(a.records) != 2 {
		t.Fatal("concurrent rotation escaped registration fence")
	}
}

func TestIssuerReplacementWithOverlappingTrust(t *testing.T) {
	s, a, oldCA := fixture(t)
	first, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Destroy()
	nextIssuer, newCA := makeIssuer(t, time.Now().Add(time.Hour), x509.ExtKeyUsageClientAuth)
	s.issuer = nextIssuer
	connection, _ := primitives.NewID(time.Now())
	peer := transport.Peer{Identity: first.Record.Identity, CertificateDigest: first.Record.CertificateDigest, ExpiresAt: first.Record.ExpiresAt}
	next, err := s.Renew(context.Background(), peer, connection, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Destroy()
	oldLeaf, newLeaf := leaf(t, first, oldCA), leaf(t, next, newCA)
	roots := x509.NewCertPool()
	roots.AddCert(oldCA)
	roots.AddCert(newCA)
	for _, c := range []*x509.Certificate{oldLeaf, newLeaf} {
		if _, err := c.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			t.Fatal("overlap rejected")
		}
	}
	roots = x509.NewCertPool()
	roots.AddCert(newCA)
	if _, err := oldLeaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("removed issuer retained trust")
	}
	if first.Record.IssuerDigest == next.Record.IssuerDigest {
		t.Fatal("issuer replacement unrecorded")
	}
}

func TestAuthenticatedRotationRequest(t *testing.T) {
	s, a, _ := fixture(t)
	bootstrap, err := s.Bootstrap(context.Background(), a.grant.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Destroy()
	peer := transport.Peer{Identity: bootstrap.Record.Identity, CertificateDigest: bootstrap.Record.CertificateDigest, ExpiresAt: bootstrap.Record.ExpiresAt}
	connection, _ := primitives.NewID(time.Now())
	if _, err := s.Rotate(context.Background(), peer, connection, 1, &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST, PrivateKeyPem: []byte("forbidden")}); err != ErrCredential {
		t.Fatal("request selected key")
	}
	rotated, err := s.Rotate(context.Background(), peer, connection, 1, &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(rotated.PrivateKeyPem)
	if rotated.Kind != agentdv1.Rotation_ISSUED || bytes.Equal(rotated.PrivateKeyPem, bootstrap.Certificate.PrivateKeyPEM) {
		t.Fatal("no fresh key")
	}
	if _, err := s.Rotate(context.Background(), peer, connection, 1, &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST}); err != ErrCredential {
		t.Fatal("predecessor renewed twice")
	}
}
