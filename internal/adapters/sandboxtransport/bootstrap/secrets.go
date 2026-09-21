// Package bootstrap projects registered ephemeral agentd material into immutable
// Kubernetes Secrets. It never lists Secrets or logs their contents/API errors.
package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

var ErrBootstrap = errors.New("agentd bootstrap projection rejected")
var ErrAbsent = errors.New("agentd bootstrap object absent")
var secrets = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

const prefix = "thinkpixel.dev/"

type Records interface {
	CheckBootstrap(context.Context, transport.CredentialRecord) error
}
type Store struct {
	client            dynamic.Interface
	namespace, domain string
	records           Records
}

func New(client dynamic.Interface, namespace, domain string, records Records) (*Store, error) {
	if client == nil || records == nil || namespace == "" || len(validation.IsDNS1123Label(namespace)) != 0 || domain == "" || net.ParseIP(domain) != nil || len(validation.IsDNS1123Subdomain(domain)) != 0 {
		return nil, ErrBootstrap
	}
	return &Store{client, namespace, domain, records}, nil
}

// Material is restricted delivery data. It is never a durable/loggable record.
// Caller owns the buffers and clears them after delivery. API/TLS copies cannot
// be guaranteed erased; no filesystem or persistent vendor-state copy is made.
type Material struct{ Config, Certificate, PrivateKey, ServerCA, Proof, Challenge []byte }

func (Material) String() string     { return "[restricted agentd bootstrap]" }
func (m Material) GoString() string { return m.String() }

// Reference is the exact non-secret identity to persist for lookup and cleanup.
// A name alone never authorizes adoption or deletion of a replacement object.
type Reference struct {
	Namespace, Name, UID, BundleDigest string
	Record                             transport.CredentialRecord
}

// Plan returns the non-secret deterministic target before an external mutation.
// Trusted composition must persist it and cleanup intent before Publish. UID is
// empty until Publish or recovery binds the exact observed object.
func (s *Store) Plan(r transport.CredentialRecord, m Material) (Reference, error) {
	data, err := s.data(r, m)
	if err != nil {
		return Reference{}, ErrBootstrap
	}
	return Reference{Namespace: s.namespace, Name: "agentd-" + string(r.CredentialID), BundleDigest: bundleDigest(data), Record: r}, nil
}

// Recover resolves only an exactly matching pre-persisted target after ambiguous
// creation, including expired/consumed credentials. It grants cleanup identity,
// not projection authority; Resolve independently requires pending registration.
func (s *Store) Recover(ctx context.Context, planned Reference) (Reference, error) {
	if planned.UID != "" || !s.validTarget(planned) {
		return Reference{}, ErrBootstrap
	}
	u, err := s.client.Resource(secrets).Namespace(s.namespace).Get(ctx, planned.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Reference{}, ErrAbsent
	}
	if err != nil || u == nil {
		return Reference{}, ErrBootstrap
	}
	var got v1.Secret
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &got) != nil || !owned(got, planned) || got.UID == "" {
		return Reference{}, ErrBootstrap
	}
	planned.UID = string(got.UID)
	return planned, nil
}

func (s *Store) Publish(ctx context.Context, r transport.CredentialRecord, m Material) (Reference, error) {
	fail := Reference{}
	data, err := s.data(r, m)
	if err != nil {
		return fail, ErrBootstrap
	}
	if s.records.CheckBootstrap(ctx, r) != nil {
		return fail, ErrBootstrap
	}
	ref := Reference{Namespace: s.namespace, Name: "agentd-" + string(r.CredentialID), BundleDigest: bundleDigest(data), Record: r}
	immutable := true
	secret := &v1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: s.namespace, Labels: labels(r), Annotations: annotations(ref)}, Immutable: &immutable, Type: v1.SecretTypeOpaque, Data: data}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		return fail, ErrBootstrap
	}
	// Create-or-verify supports an ambiguous prior create, but never updates bytes.
	u, err := s.client.Resource(secrets).Namespace(s.namespace).Create(ctx, &unstructured.Unstructured{Object: obj}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		u, err = s.client.Resource(secrets).Namespace(s.namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	}
	if err != nil || u == nil {
		return fail, ErrBootstrap
	}
	var got v1.Secret
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &got) != nil || !owned(got, ref) || got.UID == "" {
		return fail, ErrBootstrap
	}
	ref.UID = string(got.UID)
	// Publication may race consumption/fencing; never return a usable projection
	// after the registry rejects it. Exact deletion is safe even after cancellation.
	if s.records.CheckBootstrap(ctx, r) != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Delete(cleanup, ref)
		return ref, ErrBootstrap
	}
	return ref, nil
}

// Resolve returns only a Secret name for trusted blueprint composition. It never
// returns secret bytes and requires the persisted UID plus pending registration.
func (s *Store) Resolve(ctx context.Context, ref Reference) (string, error) {
	if !s.validRef(ref) || s.records.CheckBootstrap(ctx, ref.Record) != nil {
		return "", ErrBootstrap
	}
	secret, err := s.get(ctx, ref)
	if err != nil || !owned(secret, ref) || s.records.CheckBootstrap(ctx, ref.Record) != nil {
		return "", ErrBootstrap
	}
	return ref.Name, nil
}

// Delete is idempotent exact cleanup, including after authority/certificate expiry.
// UID and resourceVersion preconditions fence replacement or mutation after Get.
func (s *Store) Delete(ctx context.Context, ref Reference) error {
	if !s.validRef(ref) {
		return ErrBootstrap
	}
	secret, err := s.get(ctx, ref)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil || !owned(secret, ref) || secret.ResourceVersion == "" {
		return ErrBootstrap
	}
	uid, version := secret.UID, secret.ResourceVersion
	err = s.client.Resource(secrets).Namespace(s.namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}})
	if err != nil && !apierrors.IsNotFound(err) {
		return ErrBootstrap
	}
	return nil
}
func (s *Store) get(ctx context.Context, ref Reference) (v1.Secret, error) {
	var secret v1.Secret
	u, err := s.client.Resource(secrets).Namespace(s.namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return secret, err
	}
	if u == nil {
		return secret, ErrBootstrap
	}
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &secret) != nil || secret.UID != types.UID(ref.UID) {
		return v1.Secret{}, ErrBootstrap
	}
	return secret, nil
}
func (s *Store) validTarget(r Reference) bool {
	return r.Namespace == s.namespace && r.Name == "agentd-"+string(r.Record.CredentialID) && validRecord(r.Record) && validDigest(r.BundleDigest)
}
func (s *Store) validRef(r Reference) bool {
	return s.validTarget(r) && r.UID != "" && len(r.UID) <= 128
}
func labels(r transport.CredentialRecord) map[string]string {
	return map[string]string{prefix + "tenant": string(r.Identity.TenantID), prefix + "sandbox": string(r.Identity.SandboxID), prefix + "attempt": string(r.Identity.AttemptID), prefix + "credential": string(r.CredentialID)}
}
func annotations(r Reference) map[string]string {
	return map[string]string{prefix + "bootstrap-digest": r.BundleDigest, prefix + "expires-at": r.Record.ExpiresAt.UTC().Format(time.RFC3339Nano)}
}
func owned(s v1.Secret, r Reference) bool {
	if s.Name != r.Name || s.Namespace != r.Namespace || s.Type != v1.SecretTypeOpaque || s.Immutable == nil || !*s.Immutable || len(s.OwnerReferences) != 0 || len(s.Finalizers) != 0 || len(s.StringData) != 0 || !bounded(s.Data) || bundleDigest(s.Data) != r.BundleDigest {
		return false
	}
	for k, v := range labels(r.Record) {
		if s.Labels[k] != v {
			return false
		}
	}
	for k, v := range annotations(r) {
		if s.Annotations[k] != v {
			return false
		}
	}
	return true
}
func (s *Store) data(r transport.CredentialRecord, m Material) (map[string][]byte, error) {
	if !validRecord(r) || !r.Bootstrap || r.NotBefore.After(time.Now()) || !r.ExpiresAt.After(time.Now()) || r.ExpiresAt.Sub(r.NotBefore) > 10*time.Minute {
		return nil, ErrBootstrap
	}
	data := map[string][]byte{"config.json": m.Config, "client.crt": m.Certificate, "client.key": m.PrivateKey, "server-ca.crt": m.ServerCA, "bootstrap.proof": m.Proof, "challenge.bin": m.Challenge, "trust-domain": []byte(s.domain)}
	if !bounded(data) || digest(m.Proof) != r.ProofDigest {
		return nil, ErrBootstrap
	}
	c, err := agentd.DecodeConfig(m.Config)
	if err != nil || c.Binding.TenantId != string(r.Identity.TenantID) || c.Binding.SandboxBindingId != string(r.Identity.SandboxID) || c.Binding.AttemptId != string(r.Identity.AttemptID) {
		return nil, ErrBootstrap
	}
	pair, err := tls.X509KeyPair(m.Certificate, m.PrivateKey)
	if err != nil || len(pair.Certificate) < 2 || len(pair.Certificate) > 4 {
		return nil, ErrBootstrap
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA || !leaf.NotBefore.Equal(r.NotBefore) || !leaf.NotAfter.Equal(r.ExpiresAt) || digest(leaf.Raw) != r.CertificateDigest || digest(pair.Certificate[1]) != r.IssuerDigest || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(leaf.UnknownExtKeyUsage) != 0 || len(leaf.URIs) != 1 || len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 {
		return nil, ErrBootstrap
	}
	want := "spiffe://" + s.domain + "/tenant/" + string(r.Identity.TenantID) + "/sandbox/" + string(r.Identity.SandboxID) + "/attempt/" + string(r.Identity.AttemptID)
	if leaf.URIs[0].String() != want {
		return nil, ErrBootstrap
	}
	issuer, err := x509.ParseCertificate(pair.Certificate[1])
	if err != nil || leaf.CheckSignatureFrom(issuer) != nil {
		return nil, ErrBootstrap
	}
	roots := x509.NewCertPool()
	rest := m.ServerCA
	count := 0
	for len(bytes.TrimSpace(rest)) > 0 {
		rest = bytes.TrimSpace(rest)
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrBootstrap
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrBootstrap
		}
		ca, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !ca.IsCA || time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
			return nil, ErrBootstrap
		}
		roots.AddCert(ca)
		rest = remaining
		count++
		if count > 4 {
			return nil, ErrBootstrap
		}
	}
	if count == 0 {
		return nil, ErrBootstrap
	}
	// The server trust bundle must not also authorize this dedicated client issuer.
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		ca, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrBootstrap
		}
		intermediates.AddCert(ca)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		return nil, ErrBootstrap
	}
	for k, v := range data {
		data[k] = bytes.Clone(v)
	}
	return data, nil
}
func bounded(data map[string][]byte) bool {
	limits := map[string]int{"config.json": agentd.MaxConfigBytes, "client.crt": 16 << 10, "client.key": 4 << 10, "server-ca.crt": 16 << 10, "bootstrap.proof": 32, "challenge.bin": 32, "trust-domain": 253}
	if len(data) != len(limits) {
		return false
	}
	for k, max := range limits {
		n := len(data[k])
		if n == 0 || n > max || (k == "bootstrap.proof" || k == "challenge.bin") && n != 32 {
			return false
		}
	}
	return true
}
func validRecord(r transport.CredentialRecord) bool {
	for _, id := range []primitives.ID{r.Identity.TenantID, r.Identity.SandboxID, r.Identity.AttemptID, r.CredentialID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false
		}
	}
	return r.Bootstrap && validDigest(r.CertificateDigest) && validDigest(r.IssuerDigest) && validDigest(r.ProofDigest) && !r.NotBefore.IsZero() && r.ExpiresAt.After(r.NotBefore)
}
func validDigest(v string) bool {
	if len(v) != 71 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	b, err := hex.DecodeString(v[7:])
	return err == nil && hex.EncodeToString(b) == v[7:]
}
func digest(b []byte) string { d := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(d[:]) }

// Length-prefixed names and bytes avoid concatenation ambiguity. The digest is
// integrity metadata, not authority or a substitute for the persisted UID.
func bundleDigest(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	h := sha256.New()
	var n [8]byte
	for _, k := range keys {
		binary.BigEndian.PutUint64(n[:], uint64(len(k)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(k))
		binary.BigEndian.PutUint64(n[:], uint64(len(data[k])))
		_, _ = h.Write(n[:])
		_, _ = h.Write(data[k])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
