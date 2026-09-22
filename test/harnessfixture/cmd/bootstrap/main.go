// This test-only command makes ephemeral, unauthoritative bootstrap files for
// the image smoke test. Nothing from it is included in the supervisor image.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func ca(usage x509.ExtKeyUsage) ([]byte, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{usage}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	return der, key, err
}
func run(directory string) error {
	raw, err := os.ReadFile("deploy/agentd/config.example.json")
	if err != nil {
		return err
	}
	c, err := agentd.DecodeConfig(raw)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	c.ControlDeadlineUnixMS = now.Add(5 * time.Minute).UnixMilli()
	c.Harness.StopGraceMS = 1000
	c.Capabilities = append(c.Capabilities, "process-control.v1", "rotation.v1")
	c.RequiredCapabilities = append(c.RequiredCapabilities, "process-control.v1", "rotation.v1")
	raw, err = json.Marshal(c)
	if err != nil {
		return err
	}
	client, key, err := ca(x509.ExtKeyUsageClientAuth)
	if err != nil {
		return err
	}
	server, _, err := ca(x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	issuer, err := localissuer.New(client, key, "ar.test")
	if err != nil {
		return err
	}
	issued, err := issuer.Issue(context.Background(), transport.CertificateRequest{Identity: transport.Identity{TenantID: primitives.ID(c.Binding.TenantId), SandboxID: primitives.ID(c.Binding.SandboxBindingId), AttemptID: primitives.ID(c.Binding.AttemptId)}, NotBefore: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		return err
	}
	defer issued.Destroy()
	proof, challenge := make([]byte, 32), make([]byte, 32)
	defer clear(proof)
	defer clear(challenge)
	if _, err = rand.Read(proof); err != nil {
		return err
	}
	if _, err = rand.Read(challenge); err != nil {
		return err
	}
	files := map[string][]byte{"config.json": raw, "client.crt": issued.CertificatePEM, "client.key": issued.PrivateKeyPEM, "server-ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server}), "bootstrap.proof": proof, "challenge.bin": challenge, "trust-domain": []byte("ar.test")}
	for name, content := range files {
		f, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(content)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func main() {
	if len(os.Args) != 2 || run(os.Args[1]) != nil {
		fmt.Fprintln(os.Stderr, "image bootstrap fixture failed")
		os.Exit(1)
	}
}
