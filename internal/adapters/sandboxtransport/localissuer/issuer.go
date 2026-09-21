// Package localissuer supplies a dedicated client-only CA adapter. Its signer
// belongs to trusted AR secret management, never the sandbox or PostgreSQL.
package localissuer

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrIssuer = errors.New("agentd credential issuance rejected")

type Issuer struct {
	ca     *x509.Certificate
	signer crypto.Signer
	domain string
	mu     sync.Mutex
}

// New accepts a provisioned P-256 clientAuth-only signing CA, not a generic
// service root. The caller owns the immutable signer for this adapter's lifetime.
// No CA is generated implicitly and no file/environment discovery is performed.
func New(caDER []byte, signer crypto.Signer, domain string) (*Issuer, error) {
	ca, err := x509.ParseCertificate(bytes.Clone(caDER))
	if err != nil || signer == nil || !validDomain(domain) || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 || len(ca.ExtKeyUsage) != 1 || ca.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(ca.UnknownExtKeyUsage) != 0 {
		return nil, ErrIssuer
	}
	key, ok := ca.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, ErrIssuer
	}
	want, err := x509.MarshalPKIXPublicKey(ca.PublicKey)
	got, keyErr := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || keyErr != nil || !bytes.Equal(want, got) {
		return nil, ErrIssuer
	}
	return &Issuer{ca: ca, signer: signer, domain: domain}, nil
}

func (i *Issuer) Issue(ctx context.Context, r transport.CertificateRequest) (transport.IssuedCertificate, error) {
	fail := transport.IssuedCertificate{}
	now := time.Now()
	if ctx.Err() != nil || !identityValid(r.Identity) || r.NotBefore.IsZero() || r.NotBefore.After(now) || r.NotBefore.Before(now.Add(-time.Minute)) || !r.ExpiresAt.After(now) || r.ExpiresAt.Sub(r.NotBefore) > 15*time.Minute || now.Before(i.ca.NotBefore) || !now.Before(i.ca.NotAfter) {
		return fail, ErrIssuer
	}
	start, end := r.NotBefore.UTC().Truncate(time.Second), r.ExpiresAt.UTC().Truncate(time.Second)
	// Round the lower bound up, never backdate beyond requested authority.
	if start.Before(r.NotBefore) {
		start = start.Add(time.Second)
	}
	if start.Before(i.ca.NotBefore) {
		start = i.ca.NotBefore
	}
	if end.After(i.ca.NotAfter) {
		end = i.ca.NotAfter
	}
	if !end.After(start) || !end.After(now) {
		return fail, ErrIssuer
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fail, ErrIssuer
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err != nil {
		return fail, ErrIssuer
	}
	serial.Add(serial, big.NewInt(1))
	uri := &url.URL{Scheme: "spiffe", Host: i.domain, Path: "/tenant/" + string(r.Identity.TenantID) + "/sandbox/" + string(r.Identity.SandboxID) + "/attempt/" + string(r.Identity.AttemptID)}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: start, NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, URIs: []*url.URL{uri}}
	i.mu.Lock()
	der, err := x509.CreateCertificate(rand.Reader, template, i.ca, &key.PublicKey, i.signer)
	i.mu.Unlock()
	if err != nil || ctx.Err() != nil {
		return fail, ErrIssuer
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fail, ErrIssuer
	}
	defer clear(private)
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: i.ca.Raw})...)
	return transport.IssuedCertificate{CertificatePEM: chain, PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), CertificateDigest: digest(der), IssuerDigest: digest(i.ca.Raw), NotBefore: start, ExpiresAt: end}, nil
}

func identityValid(id transport.Identity) bool {
	for _, value := range []primitives.ID{id.TenantID, id.SandboxID, id.AttemptID} {
		if _, err := primitives.ParseID(string(value)); err != nil {
			return false
		}
	}
	return true
}
func digest(b []byte) string { d := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(d[:]) }
func validDomain(s string) bool {
	if len(s) == 0 || len(s) > 253 || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
