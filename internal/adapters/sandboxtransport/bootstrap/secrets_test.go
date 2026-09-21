package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type records struct {
	deny   bool
	calls  int
	denyAt int
}

func (r *records) CheckBootstrap(context.Context, transport.CredentialRecord) error {
	r.calls++
	if r.deny || r.denyAt != 0 && r.calls >= r.denyAt {
		return errors.New("restricted database detail")
	}
	return nil
}
func fixture(t *testing.T) (*Store, *fakeClient, *records, transport.CredentialRecord, Material) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ca := func(eku x509.ExtKeyUsage) ([]byte, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		c := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{eku}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
		der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return der, key
	}
	clientCA, key := ca(x509.ExtKeyUsageClientAuth)
	serverCA, _ := ca(x509.ExtKeyUsageServerAuth)
	raw, err := os.ReadFile("../../../../deploy/agentd/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentd.DecodeConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	id := transport.Identity{TenantID: primitives.ID(c.Binding.TenantId), SandboxID: primitives.ID(c.Binding.SandboxBindingId), AttemptID: primitives.ID(c.Binding.AttemptId)}
	issuer, err := localissuer.New(clientCA, key, "ar.test")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := issuer.Issue(ctx, transport.CertificateRequest{Identity: id, NotBefore: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cert.Destroy)
	proof := make([]byte, 32)
	if _, err = rand.Read(proof); err != nil {
		t.Fatal(err)
	}
	cid, _ := primitives.NewID(now)
	r := transport.CredentialRecord{Identity: id, CredentialID: cid, Bootstrap: true, CertificateDigest: cert.CertificateDigest, IssuerDigest: cert.IssuerDigest, ProofDigest: digest(proof), NotBefore: cert.NotBefore, ExpiresAt: cert.ExpiresAt}
	m := Material{Config: raw, Certificate: cert.CertificatePEM, PrivateKey: cert.PrivateKeyPEM, ServerCA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCA}), Proof: proof, Challenge: make([]byte, 32)}
	client := &fakeClient{objects: map[string]*unstructured.Unstructured{}}
	records := &records{}
	store, err := New(client, "sandboxes", "ar.test", records)
	if err != nil {
		t.Fatal(err)
	}
	return store, client, records, r, m
}
func TestImmutableProjectionReplayAndExactCleanup(t *testing.T) {
	s, c, _, r, m := fixture(t)
	ctx := context.Background()
	plan, err := s.Plan(r, m)
	if err != nil || plan.UID != "" {
		t.Fatal("plan must be persistable before create")
	}
	ref, err := s.Publish(ctx, r, m)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != ref.Name || plan.BundleDigest != ref.BundleDigest || ref.UID == "" {
		t.Fatal("changed plan or missing UID")
	}
	again, err := s.Publish(ctx, r, m)
	if err != nil || again != ref {
		t.Fatal("exact replay failed")
	}
	name, err := s.Resolve(ctx, ref)
	if err != nil || name != ref.Name {
		t.Fatal("resolve failed")
	}
	c.onDelete = func(_ string, opts metav1.DeleteOptions) error {
		p := opts.Preconditions
		if p == nil || p.UID == nil || string(*p.UID) != ref.UID || p.ResourceVersion == nil || *p.ResourceVersion != "1" {
			t.Fatal("unfenced delete")
		}
		return nil
	}
	if err = s.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, ref); err != nil {
		t.Fatal("cleanup not idempotent")
	}
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" || a.GetVerb() == "update" || a.GetVerb() == "patch" {
			t.Fatal("unexpected broad/mutating action")
		}
	}
}
func TestRejectMaterialBeforeKubernetes(t *testing.T) {
	for _, which := range []string{"unregistered", "proof", "key", "certificate", "scope", "expired", "ttl", "config", "challenge", "server-ca", "issuer", "shared-ca", "trailing-ca"} {
		t.Run(which, func(t *testing.T) {
			s, c, records, r, m := fixture(t)
			switch which {
			case "unregistered":
				records.deny = true
			case "proof":
				m.Proof = make([]byte, 32)
			case "key":
				m.PrivateKey = []byte("invalid")
			case "certificate":
				r.CertificateDigest = digest([]byte("other"))
			case "scope":
				r.Identity.AttemptID, _ = primitives.NewID(time.Now())
			case "expired":
				r.ExpiresAt = time.Now().Add(-time.Second)
			case "ttl":
				r.NotBefore = r.NotBefore.Add(-time.Hour)
			case "config":
				m.Config = []byte(`{"version":1}`)
			case "challenge":
				m.Challenge = nil
			case "server-ca":
				m.ServerCA = []byte("invalid")
			case "issuer":
				r.IssuerDigest = digest([]byte("other"))
			case "shared-ca":
				_, m.ServerCA = pem.Decode(m.Certificate)
			case "trailing-ca":
				m.ServerCA = append(m.ServerCA, []byte("unexpected trailing data")...)
			}
			if _, err := s.Publish(context.Background(), r, m); err != ErrBootstrap || len(c.Actions()) != 0 {
				t.Fatal("invalid material reached Kubernetes")
			}
		})
	}
}
func TestReplacementAndTamperingCannotResolveOrDelete(t *testing.T) {
	for _, which := range []string{"uid", "owner", "data", "mutable", "digest", "namespace"} {
		t.Run(which, func(t *testing.T) {
			s, c, _, r, m := fixture(t)
			ctx := context.Background()
			ref, err := s.Publish(ctx, r, m)
			if err != nil {
				t.Fatal(err)
			}
			u, err := c.Resource(secrets).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch which {
			case "uid":
				u.SetUID("replacement")
			case "owner":
				labels := u.GetLabels()
				labels[prefix+"attempt"] = "other"
				u.SetLabels(labels)
			case "data":
				_ = unstructured.SetNestedField(u.Object, "dGFtcGVyZWQ=", "data", "client.key")
			case "mutable":
				u.Object["immutable"] = false
			case "digest":
				ref.BundleDigest = digest([]byte("other"))
			case "namespace":
				ref.Namespace = "other"
			}
			c.objects["sandboxes/"+u.GetName()] = u.DeepCopy()
			c.ClearActions()
			if _, err = s.Resolve(ctx, ref); err != ErrBootstrap {
				t.Fatal("substitution resolved")
			}
			if err = s.Delete(ctx, ref); err != ErrBootstrap {
				t.Fatal("substitution deleted")
			}
			for _, a := range c.Actions() {
				if a.GetVerb() == "delete" {
					t.Fatal("unsafe delete reached API")
				}
			}
		})
	}
}
func TestRecoveryAndCleanupAfterConsumption(t *testing.T) {
	s, _, records, r, m := fixture(t)
	ctx := context.Background()
	plan, err := s.Plan(r, m)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.Publish(ctx, r, m)
	if err != nil {
		t.Fatal(err)
	}
	records.deny = true
	if _, err = s.Resolve(ctx, ref); err != ErrBootstrap {
		t.Fatal("consumed bootstrap resolved")
	}
	recovered, err := s.Recover(ctx, plan)
	if err != nil || recovered != ref {
		t.Fatal("ambiguous create could not recover exact identity")
	}
	if err = s.Delete(ctx, recovered); err != nil {
		t.Fatal("cleanup incorrectly required live authority")
	}
	if _, err = s.Recover(ctx, plan); err != ErrAbsent {
		t.Fatal("confirmed absence not distinguished from failure")
	}
}
func TestConsumptionRacingPublicationCleansExactObject(t *testing.T) {
	s, c, records, r, m := fixture(t)
	records.denyAt = 2
	ref, err := s.Publish(context.Background(), r, m)
	if err != ErrBootstrap || ref.UID == "" {
		t.Fatal("post-create fence failure lacks cleanup identity")
	}
	if _, err = c.Resource(secrets).Namespace(ref.Namespace).Get(context.Background(), ref.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("fenced projection not removed")
	}
}
func TestDeleteConflictFailsClosedAndSanitizes(t *testing.T) {
	s, c, _, r, m := fixture(t)
	ctx := context.Background()
	ref, err := s.Publish(ctx, r, m)
	if err != nil {
		t.Fatal(err)
	}
	c.onDelete = func(_ string, _ metav1.DeleteOptions) error {
		return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, ref.Name, errors.New("restricted API detail"))
	}
	if err = s.Delete(ctx, ref); err != ErrBootstrap {
		t.Fatal("delete conflict not sanitized")
	}
}
