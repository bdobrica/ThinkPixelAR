package localissuer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"math/big"
	"testing"
	"time"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestRejectNonDedicatedIssuer(t *testing.T) {
	for _, which := range []string{"unrestricted", "server", "both", "not-ca", "no-cert-sign", "key-mismatch", "domain", "malformed", "nil-signer"} {
		t.Run(which, func(t *testing.T) {
			key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			c := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
			domain := "ar.test"
			switch which {
			case "unrestricted":
				c.ExtKeyUsage = nil
			case "server":
				c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			case "both":
				c.ExtKeyUsage = append(c.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
			case "not-ca":
				c.IsCA = false
			case "no-cert-sign":
				c.KeyUsage = x509.KeyUsageDigitalSignature
			case "domain":
				domain = "ar.test/other"
			}
			der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			if which == "key-mismatch" {
				key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			}
			if which == "malformed" {
				der = []byte("invalid")
			}
			if which == "nil-signer" {
				if _, err := New(der, nil, domain); err != ErrIssuer {
					t.Fatal("nil signer accepted")
				}
				return
			}
			if _, err := New(der, key, domain); err != ErrIssuer {
				t.Fatal("unsafe issuer accepted")
			}
		})
	}
}
func TestRejectUnboundedCertificateRequests(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now().UTC().Truncate(time.Second)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	i, err := New(der, key, "ar.test")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := primitives.NewID(now)
	for _, which := range []string{"ttl", "expired", "future", "backdated", "identity", "cancelled"} {
		t.Run(which, func(t *testing.T) {
			r := transport.CertificateRequest{Identity: transport.Identity{TenantID: id, SandboxID: id, AttemptID: id}, NotBefore: now, ExpiresAt: now.Add(10 * time.Minute)}
			ctx := context.Background()
			switch which {
			case "ttl":
				r.ExpiresAt = now.Add(16 * time.Minute)
			case "expired":
				r.ExpiresAt = now
			case "future":
				r.NotBefore = now.Add(time.Minute)
			case "backdated":
				r.NotBefore = now.Add(-2 * time.Minute)
			case "identity":
				r.Identity.AttemptID = "invalid"
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			got, err := i.Issue(ctx, r)
			if err != ErrIssuer || len(got.PrivateKeyPEM) != 0 {
				t.Fatal("invalid request yielded a credential")
			}
		})
	}
}
