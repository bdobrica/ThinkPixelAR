package agentd

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"google.golang.org/protobuf/proto"
)

// Connected discards the one-time proof after an authenticated Welcome. Calls
// and credential installation are serialized by the supervisor's stream loop.
func (b *TransportBootstrap) Connected(s *grpctransport.Session) error {
	if b == nil || s == nil || s.Context().Err() != nil || !proto.Equal(s.Welcome().Binding, b.config.Binding) {
		return ErrConfig
	}
	w := s.Welcome()
	if w.ConnectionEpoch <= b.connectionEpoch {
		return ErrConfig
	}
	b.connectionID = w.ConnectionId
	b.connectionEpoch = w.ConnectionEpoch
	clear(b.proof)
	b.proof = nil
	return nil
}

// InstallRotation accepts only an ISSUED frame received on the current admitted
// stream. The dispatcher must pass the actual result of Session.Recv. This is
// ephemeral replacement, not serialization, persistence or authority issuance.
func (b *TransportBootstrap) InstallRotation(s *grpctransport.Session, f *agentdv1.Envelope) error {
	if b == nil || s == nil || s.Context().Err() != nil || f == nil || !protocol.KnownFields(f) {
		return ErrConfig
	}
	w := s.Welcome()
	if w.ConnectionId != b.connectionID || w.ConnectionEpoch != b.connectionEpoch || f.ConnectionId != w.ConnectionId || f.ConnectionEpoch != w.ConnectionEpoch || !proto.Equal(f.Binding, b.config.Binding) || !proto.Equal(w.Binding, b.config.Binding) {
		return ErrConfig
	}
	r := f.GetRotation()
	if r == nil || r.Kind != agentdv1.Rotation_ISSUED || len(r.CertificatePem) == 0 || len(r.CertificatePem) > 16<<10 || len(r.PrivateKeyPem) == 0 || len(r.PrivateKeyPem) > 4<<10 {
		return ErrConfig
	}
	pair, err := tls.X509KeyPair(r.CertificatePem, r.PrivateKeyPem)
	if err != nil || len(pair.Certificate) < 2 || len(pair.Certificate) > 4 || grpctransport.ValidateSessionIdentity(pair, b.roots, b.domain, b.config.Binding) != nil || verifyClientChain(pair) != nil {
		return ErrConfig
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.NotAfter.UnixMilli() != r.ExpiresUnixMs {
		return ErrConfig
	}
	old, err := x509.ParseCertificate(b.certificate.Certificate[0])
	if err != nil || bytes.Equal(leaf.RawSubjectPublicKeyInfo, old.RawSubjectPublicKeyInfo) {
		return ErrConfig
	}
	for _, der := range b.certificate.Certificate {
		clear(der)
	}
	b.certificate = pair
	b.sessionCredential = true
	clear(b.proof)
	b.proof = nil
	return nil
}

// RotationAt requests a session identity immediately after bootstrap, then
// requests renewal 30 seconds before expiry. The stream owner serializes the
// request with other outbound frames and reconnects after installing the reply.
func (b *TransportBootstrap) RotationAt() time.Time {
	if !b.sessionCredential {
		return time.Now()
	}
	leaf, err := x509.ParseCertificate(b.certificate.Certificate[0])
	if err != nil {
		return time.Now()
	}
	return leaf.NotAfter.Add(-30 * time.Second)
}
