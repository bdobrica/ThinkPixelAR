package http

// Test-only credential composition for the standalone scenario. The production
// issuer and registry are real; this fixture supplies current local authority.
// It does not replace provider qualification or authenticated wire admission.
import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/authority/local"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
)

type e2eCredentialAuthority struct {
	store    *postgres.Store
	bindings *postgres.SandboxBindings
	registry *postgres.AgentdCredentials
	local    *local.Authority
	caller   authority.Caller
}

func (a e2eCredentialAuthority) AuthorizeCredential(ctx context.Context, r transport.CredentialRequest) (transport.CredentialGrant, error) {
	fail := transport.CredentialGrant{}
	if r.Identity.TenantID != a.caller.TenantID || r.Recovery || r.Peer != (transport.Peer{}) || r.Epoch != 0 || r.ConnectionID != "" {
		return fail, authority.ErrDenied
	}
	i, err := a.bindings.LoadCompute(ctx, r.Identity.TenantID, r.Identity.SandboxID)
	if err != nil || !i.Current || i.Desired != sandbox.ComputeRunning || i.Binding.Request.Scope.AttemptID != r.Identity.AttemptID {
		return fail, authority.ErrDenied
	}
	var g authority.Grant
	err = a.store.WithinTransaction(ctx, a.caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		_, grant, err := executions.LoadLocalBinding(ctx, repos, i.Binding.Request.Scope.ExecutionID)
		g = grant
		return err
	})
	if err != nil {
		return fail, authority.ErrDenied
	}
	status, err := a.local.Validate(ctx, a.caller, g)
	if err != nil || status.State != authority.Active || g.Generation != i.Binding.Request.Scope.Generation {
		return fail, authority.ErrDenied
	}
	v, err := a.registry.Version(ctx, r.Identity)
	if err != nil {
		return fail, err
	}
	return transport.CredentialGrant{Identity: r.Identity, Version: v, AuthorityDeadline: g.ExpiresAt, AttemptDeadline: i.Binding.Request.Deadline, BootstrapDeadline: g.ExpiresAt}, nil
}

func (a e2eCredentialAuthority) CommitCredential(ctx context.Context, r transport.CredentialRequest, g transport.CredentialGrant, record transport.CredentialRecord) error {
	fresh, err := a.AuthorizeCredential(ctx, r)
	if err != nil || fresh != g {
		return authority.ErrDenied
	}
	return a.registry.Register(ctx, r, fresh, record)
}

func e2eCredentialIssuer(t *testing.T) *localissuer.Issuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	e2eCheck(t, err)
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	e2eCheck(t, err)
	issuer, err := localissuer.New(der, key, "e2e003.invalid")
	e2eCheck(t, err)
	return issuer
}

func e2eCredentialPeer(t *testing.T, d agentdidentity.Delivery, g authority.Grant) transport.Peer {
	t.Helper()
	// Verify the actual delivered certificate/key, not merely registration IDs.
	pair, err := tls.X509KeyPair(d.Certificate.CertificatePEM, d.Certificate.PrivateKeyPEM)
	e2eCheck(t, err)
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	e2eCheck(t, err)
	ca, err := x509.ParseCertificate(pair.Certificate[1])
	e2eCheck(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	e2eCheck(t, err)
	id := d.Record.Identity
	want := "spiffe://e2e003.invalid/tenant/" + string(id.TenantID) + "/sandbox/" + string(id.SandboxID) + "/attempt/" + string(id.AttemptID)
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != want || !leaf.NotAfter.Equal(d.Record.ExpiresAt) || leaf.NotAfter.After(g.ExpiresAt) || len(d.Proof) != 32 {
		t.Fatal("credential binding or lifetime mismatch")
	}
	return transport.Peer{Identity: id, CertificateDigest: d.Record.CertificateDigest, ExpiresAt: d.Record.ExpiresAt}
}

func e2eExcludeCredential(t *testing.T, raw []byte, d agentdidentity.Delivery) {
	t.Helper()
	for _, secret := range [][]byte{d.Certificate.PrivateKeyPEM, d.Proof, []byte(base64.StdEncoding.EncodeToString(d.Proof)), []byte(hex.EncodeToString(d.Proof))} {
		if len(secret) == 0 || bytes.Contains(raw, secret) {
			t.Fatal("credential material in durable export")
		}
	}
}
