// Package grpctransport implements the agentd-initiated TLS 1.3 gRPC channel.
package grpctransport

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrTransport = errors.New("agentd transport rejected")

func clientPeer(cert *x509.Certificate, domain string, now time.Time) (transport.Peer, error) {
	fail := transport.Peer{}
	if !strongLeaf(cert, x509.ExtKeyUsageClientAuth, now) || cert.NotAfter.Sub(cert.NotBefore) > 15*time.Minute || len(cert.URIs) != 1 || len(cert.DNSNames) != 0 || len(cert.IPAddresses) != 0 || len(cert.EmailAddresses) != 0 {
		return fail, ErrTransport
	}
	u := cert.URIs[0]
	if u.Scheme != "spiffe" || u.Host != domain || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return fail, ErrTransport
	}
	p := strings.Split(u.Path, "/")
	if len(p) != 7 || p[0] != "" || p[1] != "tenant" || p[3] != "sandbox" || p[5] != "attempt" {
		return fail, ErrTransport
	}
	tenant, e1 := primitives.ParseID(p[2])
	sandbox, e2 := primitives.ParseID(p[4])
	attempt, e3 := primitives.ParseID(p[6])
	if e1 != nil || e2 != nil || e3 != nil {
		return fail, ErrTransport
	}
	digest := sha256.Sum256(cert.Raw)
	return transport.Peer{Identity: transport.Identity{TenantID: tenant, SandboxID: sandbox, AttemptID: attempt}, CertificateDigest: "sha256:" + hex.EncodeToString(digest[:]), ExpiresAt: cert.NotAfter}, nil
}
func strongLeaf(c *x509.Certificate, eku x509.ExtKeyUsage, now time.Time) bool {
	if c == nil || c.IsCA || now.Before(c.NotBefore) || !now.Before(c.NotAfter) || len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != eku || len(c.UnknownExtKeyUsage) != 0 || c.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false
	}
	return strongKey(c)
}
func strongKey(c *x509.Certificate) bool {
	switch key := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		return key.Curve != nil && key.Curve.Params().BitSize >= 256
	case ed25519.PublicKey:
		return len(key) == ed25519.PublicKeySize
	case *rsa.PublicKey:
		return key.N.BitLen() >= 3072
	default:
		return false
	}
}
func serverLeaf(c *x509.Certificate, name string, now time.Time) bool {
	return strongLeaf(c, x509.ExtKeyUsageServerAuth, now) && len(c.DNSNames) == 1 && c.DNSNames[0] == name && len(c.URIs) == 0 && len(c.IPAddresses) == 0 && len(c.EmailAddresses) == 0 && c.VerifyHostname(name) == nil
}
func dnsName(name string) bool {
	if len(name) == 0 || len(name) > 253 || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
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
func endpoint(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !dnsName(name) || u.Scheme != "https" || u.Hostname() != name || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
		return "", ErrTransport
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(name, port), nil
}
func certificate(c tls.Certificate) (tls.Certificate, *x509.Certificate, error) {
	if len(c.Certificate) == 0 || c.PrivateKey == nil {
		return tls.Certificate{}, nil, ErrTransport
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, ErrTransport
	}
	// Own DER slices; caller mutation must not change identity after construction.
	der := make([][]byte, len(c.Certificate))
	for i, b := range c.Certificate {
		der[i] = append([]byte(nil), b...)
	}
	c.Certificate = der
	c.Leaf = leaf
	return c, leaf, nil
}

func strongChains(chains [][]*x509.Certificate) bool {
	for _, chain := range chains {
		valid := len(chain) > 0
		for _, c := range chain {
			if c == nil || !strongKey(c) {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}

// Reject a trust bundle that also validates the local role's chain: the client
// issuer must not double as server trust. EKU constraints remain part of Verify.
func crossTrusted(c tls.Certificate, roots *x509.CertPool, usage x509.ExtKeyUsage) bool {
	if roots == nil || c.Leaf == nil {
		return false
	}
	intermediate := x509.NewCertPool()
	for _, raw := range c.Certificate[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return true
		}
		intermediate.AddCert(cert)
	}
	_, err := c.Leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediate, KeyUsages: []x509.ExtKeyUsage{usage}})
	return err == nil
}
