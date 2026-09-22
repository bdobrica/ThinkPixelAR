package agentd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"slices"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"google.golang.org/protobuf/proto"
)

var bootstrapFiles = []struct {
	name string
	max  int
}{
	{"config.json", MaxConfigBytes}, {"client.crt", 16 << 10}, {"client.key", 4 << 10},
	{"server-ca.crt", 16 << 10}, {"bootstrap.proof", 32}, {"challenge.bin", 32}, {"trust-domain", 253},
}

// TransportBootstrap owns ephemeral parsed credential material. It is not
// execution authority. It must not be logged, serialized or copied into state.
type TransportBootstrap struct {
	config            Config
	connectionID      string
	connectionEpoch   uint64
	sessionCredential bool
	certificate       tls.Certificate
	roots             *x509.CertPool
	domain            string
	proof, challenge  []byte
}

func (*TransportBootstrap) String() string     { return "[restricted agentd bootstrap]" }
func (b *TransportBootstrap) GoString() string { return b.String() }

// Destroy clears owned byte buffers and drops parsed private-key references.
// Go/TLS copies and big integers cannot be guaranteed erased. Call after closing
// clients using this bootstrap; concurrent use and destruction are not supported.
func (b *TransportBootstrap) Destroy() {
	if b == nil {
		return
	}
	clear(b.proof)
	clear(b.challenge)
	for _, der := range b.certificate.Certificate {
		clear(der)
	}
	*b = TransportBootstrap{}
}

// Config returns a private copy of the validated non-secret configuration.
func (b *TransportBootstrap) Config() Config {
	c := b.config
	if c.Binding != nil {
		c.Binding = proto.Clone(c.Binding).(*agentdv1.Binding)
	}
	if c.Protocol != nil {
		c.Protocol = proto.Clone(c.Protocol).(*agentdv1.VersionRange)
	}
	if c.Limits != nil {
		c.Limits = proto.Clone(c.Limits).(*agentdv1.Limits)
	}
	c.Capabilities = slices.Clone(c.Capabilities)
	c.RequiredCapabilities = slices.Clone(c.RequiredCapabilities)
	c.Harness.Argv = slices.Clone(c.Harness.Argv)
	return c
}

// ClientConfig requires the supervisor's real frame handler. Loading credentials
// never installs a permissive Check or initiates network/process work.
func (b *TransportBootstrap) ClientConfig(check func(context.Context, *agentdv1.Envelope) error) (grpctransport.ClientConfig, error) {
	if b == nil || check == nil || b.roots == nil || (len(b.proof) != 0 && len(b.proof) != 32) || grpctransport.ValidateSessionIdentity(b.certificate, b.roots, b.domain, b.config.Binding) != nil {
		return grpctransport.ClientConfig{}, ErrConfig
	}
	c := b.Config()
	e := protocol.Expected{Binding: c.Binding, Challenge: bytes.Clone(b.challenge), BuildDigest: c.BuildDigest, AdapterKind: c.AdapterKind, AdapterDigest: c.AdapterDigest, SupportedCapabilities: slices.Clone(c.Capabilities), RequiredCapabilities: slices.Clone(c.RequiredCapabilities), Limits: c.Limits}
	h := &agentdv1.Hello{Versions: c.Protocol, Binding: proto.Clone(c.Binding).(*agentdv1.Binding), Challenge: bytes.Clone(b.challenge), BootstrapProof: bytes.Clone(b.proof), BuildDigest: c.BuildDigest, AdapterKind: c.AdapterKind, AdapterDigest: c.AdapterDigest, SupportedCapabilities: slices.Clone(c.Capabilities), RequiredCapabilities: slices.Clone(c.RequiredCapabilities), Limits: proto.Clone(c.Limits).(*agentdv1.Limits)}
	cert := b.certificate
	cert.Certificate = make([][]byte, len(b.certificate.Certificate))
	for i, der := range b.certificate.Certificate {
		cert.Certificate[i] = bytes.Clone(der)
	}
	return grpctransport.ClientConfig{Endpoint: c.Endpoint, ServerName: c.ServerName, TrustDomain: b.domain, Certificate: cert, ServerRoots: b.roots.Clone(), Expected: e, Hello: h, Check: check}, nil
}

func decodeTransport(files map[string][]byte) (*TransportBootstrap, error) {
	if len(files) != len(bootstrapFiles) {
		return nil, ErrConfig
	}
	for _, f := range bootstrapFiles {
		if len(files[f.name]) == 0 || len(files[f.name]) > f.max {
			return nil, ErrConfig
		}
	}
	if len(files["bootstrap.proof"]) != 32 || len(files["challenge.bin"]) != 32 {
		return nil, ErrConfig
	}
	c, err := DecodeConfig(files["config.json"])
	if err != nil {
		return nil, ErrConfig
	}
	pair, err := tls.X509KeyPair(files["client.crt"], files["client.key"])
	if err != nil || len(pair.Certificate) < 2 || len(pair.Certificate) > 4 {
		return nil, ErrConfig
	}
	roots := x509.NewCertPool()
	raw := files["server-ca.crt"]
	count := 0
	for len(bytes.TrimSpace(raw)) > 0 {
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrConfig
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrConfig
		}
		ca, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !ca.IsCA || time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
			return nil, ErrConfig
		}
		roots.AddCert(ca)
		count++
		if count > 4 {
			return nil, ErrConfig
		}
		raw = rest
	}
	if count == 0 || grpctransport.ValidateBootstrapIdentity(pair, roots, string(files["trust-domain"]), c.Binding) != nil {
		return nil, ErrConfig
	}
	// Verify the delivered client chain's signature/EKU/time consistency. Its root
	// is bootstrap-supplied, not AR trust: AR independently verifies its own roots.
	if verifyClientChain(pair) != nil {
		return nil, ErrConfig
	}
	return &TransportBootstrap{config: c, certificate: pair, roots: roots, domain: string(files["trust-domain"]), proof: bytes.Clone(files["bootstrap.proof"]), challenge: bytes.Clone(files["challenge.bin"])}, nil
}

func verifyClientChain(pair tls.Certificate) error {
	issuer, err := x509.ParseCertificate(pair.Certificate[len(pair.Certificate)-1])
	if err != nil {
		return ErrConfig
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(issuer)
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1 : len(pair.Certificate)-1] {
		ca, err := x509.ParseCertificate(der)
		if err != nil {
			return ErrConfig
		}
		intermediates.AddCert(ca)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return ErrConfig
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: clientRoots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return ErrConfig
	}
	return nil
}
