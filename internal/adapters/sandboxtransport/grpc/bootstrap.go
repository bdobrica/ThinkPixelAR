package grpctransport

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
)

// ValidateBootstrapIdentity validates local material without dialing or granting
// authority. Connect independently repeats the transport profile checks.
func ValidateBootstrapIdentity(c tls.Certificate, roots *x509.CertPool, domain string, b *agentdv1.Binding) error {
	return validateIdentity(c, roots, domain, b, 10*time.Minute)
}

// ValidateSessionIdentity validates a renewed identity without granting authority.
func ValidateSessionIdentity(c tls.Certificate, roots *x509.CertPool, domain string, b *agentdv1.Binding) error {
	return validateIdentity(c, roots, domain, b, 15*time.Minute)
}
func validateIdentity(c tls.Certificate, roots *x509.CertPool, domain string, b *agentdv1.Binding, ttl time.Duration) error {
	cert, leaf, err := certificate(c)
	if err != nil || roots == nil || !dnsName(domain) || b == nil || leaf.NotAfter.Sub(leaf.NotBefore) > ttl || crossTrusted(cert, roots, x509.ExtKeyUsageClientAuth) {
		return ErrTransport
	}
	p, err := clientPeer(leaf, domain, time.Now())
	if err != nil || string(p.Identity.TenantID) != b.TenantId || string(p.Identity.SandboxID) != b.SandboxBindingId || string(p.Identity.AttemptID) != b.AttemptId {
		return ErrTransport
	}
	return nil
}
