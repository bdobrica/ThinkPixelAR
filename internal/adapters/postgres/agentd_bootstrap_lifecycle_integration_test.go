package postgres_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdbootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func bootstrapCA(t *testing.T, usage x509.ExtKeyUsage, ttl time.Duration) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{usage}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(ttl)}
	der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

type bootstrapProvider struct {
	*admissionFixture
	calls, denyAt int
}

func (p *bootstrapProvider) Get(ctx context.Context, tenant, id primitives.ID) (sandbox.Status, error) {
	p.calls++
	if p.denyAt != 0 && p.calls >= p.denyAt {
		return sandbox.Status{}, errors.New("provider observation failed")
	}
	return p.admissionFixture.Get(ctx, tenant, id)
}
func TestAgentdBootstrapLifecycle(t *testing.T) {
	for _, mode := range []string{"acceptance", "post-consumption-failure", "projection-failure", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			db, policy, m, intent, _ := policyFixture(t, true)
			ctx := context.Background()
			scope := intent.Binding.Request.Scope
			id := transport.Identity{TenantID: scope.TenantID, SandboxID: scope.SandboxID, AttemptID: scope.AttemptID}
			registry, _ := postgres.NewAgentdCredentials(db)
			bindings, _ := postgres.NewSandboxBindings(db)
			provider := &bootstrapProvider{admissionFixture: &admissionFixture{r: intent.Binding.Request}}
			admission, err := agentdadmission.New(bindings, provider, registry, policy, policy)
			if err != nil {
				t.Fatal(err)
			}
			ttl := time.Hour
			if mode == "expiry" {
				ttl = 3 * time.Second
			}
			ca, key := bootstrapCA(t, x509.ExtKeyUsageClientAuth, ttl)
			serverCA, _ := bootstrapCA(t, x509.ExtKeyUsageServerAuth, time.Hour)
			issuer, err := localissuer.New(ca, key, "ar.test")
			if err != nil {
				t.Fatal(err)
			}
			identity, _ := agentdidentity.New(issuer, admission)
			client := &bootstrapTestClient{objects: map[string]*unstructured.Unstructured{}}
			gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
			secrets, _ := bootstrap.New(client, "sandboxes", "ar.test", registry)
			journal, _ := postgres.NewAgentdDelivery(db)
			delivery, _ := bootstrap.NewDelivery(secrets, journal, func(ctx context.Context, r transport.CredentialRecord) error {
				_, err := admission.AuthorizeCredential(ctx, transport.CredentialRequest{Identity: r.Identity})
				return err
			})
			service, _ := agentdbootstrap.New(identity, delivery)
			config, _ := json.Marshal(m.Config)
			var ref bootstrap.Reference
			err = service.Materialize(ctx, id, config, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCA}), m.Challenge, func(ctx context.Context, name string) error {
				cid, err := primitives.ParseID(strings.TrimPrefix(name, "agentd-"))
				if err != nil {
					return err
				}
				entry, err := journal.LoadDelivery(ctx, id.TenantID, cid)
				if err != nil {
					return err
				}
				ref = entry.Reference
				if ref.UID != "exact-uid" || entry.CleanupRequested {
					t.Fatal("projection before durable UID")
				}
				if mode == "projection-failure" {
					return errors.New("acquisition failed")
				}
				return nil
			})
			if (err != nil) != (mode == "projection-failure") {
				t.Fatal("materialization", err)
			}
			if mode == "acceptance" || mode == "post-consumption-failure" {
				obj, err := client.Resource(gvr).Namespace("sandboxes").Get(ctx, ref.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				// The API double stores base64-encoded Secret data; decode through JSON.
				var secret struct {
					Data map[string][]byte `json:"data"`
				}
				raw, _ := json.Marshal(obj.Object)
				if json.Unmarshal(raw, &secret) != nil {
					t.Fatal("bundle")
				}
				proof := secret.Data["bootstrap.proof"]
				peer := transport.Peer{Identity: id, CertificateDigest: ref.Record.CertificateDigest, ExpiresAt: ref.Record.ExpiresAt}
				wrong := append([]byte(nil), proof...)
				wrong[0] ^= 1
				if _, err = admission.Admit(ctx, peer, wrong); err == nil {
					t.Fatal("bad proof accepted")
				}
				entry, _ := journal.LoadDelivery(ctx, id.TenantID, ref.Record.CredentialID)
				if entry.CleanupRequested {
					t.Fatal("bad proof triggered deletion")
				}
				// A post-consumption failure closes the accepted epoch; durable cleanup
				// must already exist and survive closure/reconstruction.
				provider.calls = 0
				if mode == "post-consumption-failure" {
					provider.denyAt = 2
				}
				lease, err := admission.Admit(ctx, peer, proof)
				clear(proof)
				if mode == "post-consumption-failure" {
					if err == nil {
						lease.Close()
						t.Fatal("post-consumption provider failure accepted")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					lease.Close()
				}
				if mode == "post-consumption-failure" {
					if err = policy.Revoke(ctx, id.TenantID, id.SandboxID); err != nil {
						t.Fatal(err)
					}
				}
				entry, err = journal.LoadDelivery(ctx, id.TenantID, ref.Record.CredentialID)
				if err != nil || !entry.CleanupRequested {
					t.Fatal("consumption lost cleanup", err)
				}
			}
			if mode == "expiry" {
				timer := time.NewTimer(time.Until(ref.Record.ExpiresAt) + 20*time.Millisecond)
				<-timer.C
			}
			// Reconstruct the coordinator and run the actual periodic worker. No fresh
			// authority is needed to delete consumed, revoked or expired material.
			journal, _ = postgres.NewAgentdDelivery(db)
			restarted, _ := bootstrap.NewDelivery(secrets, journal, func(context.Context, transport.CredentialRecord) error { return errors.New("no authority") })
			run, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			worker, _ := agentdbootstrap.NewWorker(restarted, []primitives.ID{id.TenantID}, 5*time.Second, time.Second, 8, slog.New(slog.NewTextHandler(io.Discard, nil)))
			go func() { done <- worker.Run(run) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(2 * time.Second)
			for {
				entry, err := journal.LoadDelivery(ctx, id.TenantID, ref.Record.CredentialID)
				if err != nil {
					t.Fatal(err)
				}
				if entry.Cleaned {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not clean delivery")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err = client.Resource(gvr).Namespace("sandboxes").Get(ctx, ref.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatal("Secret remains", err)
			}
		})
	}
}
