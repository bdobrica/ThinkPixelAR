package agentd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func transportFixture(t *testing.T) map[string][]byte { return transportFixtureTTL(t, 5*time.Minute) }
func transportFixtureTTL(t *testing.T, ttl time.Duration) map[string][]byte {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	ca := func(eku x509.ExtKeyUsage) ([]byte, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{eku}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return der, key
	}
	der, key := ca(x509.ExtKeyUsageClientAuth)
	server, _ := ca(x509.ExtKeyUsageServerAuth)
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := localissuer.New(der, key, "ar.test")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuer.Issue(context.Background(), transport.CertificateRequest{Identity: transport.Identity{TenantID: primitives.ID(c.Binding.TenantId), SandboxID: primitives.ID(c.Binding.SandboxBindingId), AttemptID: primitives.ID(c.Binding.AttemptId)}, NotBefore: now, ExpiresAt: now.Add(ttl)})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"config.json": fixture(t), "client.crt": issued.CertificatePEM, "client.key": issued.PrivateKeyPEM, "server-ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server}), "bootstrap.proof": make([]byte, 32), "challenge.bin": make([]byte, 32), "trust-domain": []byte("ar.test")}
	if _, err = rand.Read(files["bootstrap.proof"]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, raw := range files {
			clear(raw)
		}
	})
	return files
}
func denyFrame(context.Context, *agentdv1.Envelope) error { return ErrConfig }
func TestTransportCredentialsDecodeAndOwnership(t *testing.T) {
	files := transportFixture(t)
	b, err := decodeTransport(files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ClientConfig(nil); err != ErrConfig {
		t.Fatal("nil frame check accepted")
	}
	c, err := b.ClientConfig(denyFrame)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.Hello.BootstrapProof, files["bootstrap.proof"]) || len(c.Expected.Challenge) != 32 || c.TrustDomain != "ar.test" {
		t.Fatal("wrong client material")
	}
	c.Hello.BootstrapProof[0] ^= 1
	c.Expected.Binding.AttemptId = "changed"
	copy := b.Config()
	copy.Harness.Argv[0] = "changed"
	copy.Binding.SessionGeneration++
	next, err := b.ClientConfig(denyFrame)
	if err != nil || !bytes.Equal(next.Hello.BootstrapProof, files["bootstrap.proof"]) || next.Expected.Binding.AttemptId == "changed" || b.Config().Harness.Argv[0] == "changed" {
		t.Fatal("caller mutation leaked into bootstrap")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, b) != "[restricted agentd bootstrap]" {
			t.Fatal("formatting exposed material")
		}
	}
	proof := b.proof
	b.Destroy()
	if !bytes.Equal(proof, make([]byte, len(proof))) {
		t.Fatal("owned proof not cleared")
	}
	if _, err = b.ClientConfig(denyFrame); err != ErrConfig {
		t.Fatal("destroyed material remained usable")
	}
}
func TestTransportCredentialsRejectMalformedBundle(t *testing.T) {
	for _, which := range []string{"missing", "extra", "oversized", "proof", "challenge", "key", "certificate", "domain", "cross-binding", "shared-ca", "trailing-ca", "ca-count"} {
		t.Run(which, func(t *testing.T) {
			files := transportFixture(t)
			switch which {
			case "missing":
				delete(files, "client.key")
			case "extra":
				files["other"] = []byte("x")
			case "oversized":
				files["client.key"] = make([]byte, (4<<10)+1)
			case "proof":
				files["bootstrap.proof"] = make([]byte, 31)
			case "challenge":
				files["challenge.bin"] = nil
			case "key":
				files["client.key"] = []byte("invalid")
			case "certificate":
				files["client.crt"] = []byte("invalid")
			case "domain":
				files["trust-domain"] = []byte("other.test")
			case "cross-binding":
				files["config.json"] = bytes.ReplaceAll(files["config.json"], []byte("01950000-0000-7000-8000-000000000001"), []byte("01950000-0000-7000-8000-000000000002"))
			case "shared-ca":
				_, files["server-ca.crt"] = pem.Decode(files["client.crt"])
			case "trailing-ca":
				files["server-ca.crt"] = append(files["server-ca.crt"], []byte("invalid")...)
			case "ca-count":
				files["server-ca.crt"] = bytes.Repeat(files["server-ca.crt"], 5)
			}
			if b, err := decodeTransport(files); err != ErrConfig || b != nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

func TestTransportRejectsSessionTTLAsBootstrap(t *testing.T) {
	if b, err := decodeTransport(transportFixtureTTL(t, 12*time.Minute)); err != ErrConfig || b != nil {
		t.Fatal("session TTL accepted as bootstrap")
	}
}
