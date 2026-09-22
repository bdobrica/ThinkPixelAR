package grpctransport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/proto"
)

type ClientConfig struct {
	Endpoint, ServerName, TrustDomain string
	Certificate                       tls.Certificate
	ServerRoots                       *x509.CertPool
	Expected                          protocol.Expected
	Hello                             *agentdv1.Hello
	Check                             func(context.Context, *agentdv1.Envelope) error
}

// Connect initiates the only network direction. It accepts explicit roots and
// identity, never system roots, proxy environment, insecure flags or discovery.
// An empty Hello proof selects credential reconnect; a 32-byte proof is bootstrap.
func Connect(ctx context.Context, c ClientConfig) (*Session, error) {
	return connect(ctx, c, nil)
}

// dial is an internal test seam; production always uses the exact DNS endpoint.
func connect(ctx context.Context, c ClientConfig, dial func(context.Context, string) (net.Conn, error)) (*Session, error) {
	target, err := endpoint(c.Endpoint, c.ServerName)
	cert, leaf, certErr := certificate(c.Certificate)
	if err != nil || certErr != nil || c.ServerRoots == nil || crossTrusted(cert, c.ServerRoots, x509.ExtKeyUsageClientAuth) || !dnsName(c.TrustDomain) || c.Hello == nil || (len(c.Hello.BootstrapProof) != 0 && len(c.Hello.BootstrapProof) != 32) || c.Check == nil {
		return nil, ErrTransport
	}
	identity, err := clientPeer(leaf, c.TrustDomain, time.Now())
	if err != nil {
		return nil, ErrTransport
	}
	b := c.Expected.Binding
	if b == nil || b.TenantId != string(identity.Identity.TenantID) || b.SandboxBindingId != string(identity.Identity.SandboxID) || b.AttemptId != string(identity.Identity.AttemptID) {
		return nil, ErrTransport
	}
	// Validate before dialing; the real connection ID/epoch arrive only in Welcome.
	if _, err = protocol.Negotiate(c.Hello, c.Expected, b.SandboxBindingId, 1); err != nil {
		return nil, ErrTransport
	}
	var peerMu sync.Mutex
	var peerExpiry time.Time
	config := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, RootCAs: c.ServerRoots.Clone(), ServerName: c.ServerName, Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "h2" || !strongChains(state.VerifiedChains) || len(state.PeerCertificates) == 0 || !serverLeaf(state.PeerCertificates[0], c.ServerName, time.Now()) {
			return ErrTransport
		}
		peerMu.Lock()
		peerExpiry = state.PeerCertificates[0].NotAfter
		peerMu.Unlock()
		return nil
	}
	if dial == nil {
		dial = func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
		}
	}
	// An explicit dialer avoids HTTP_PROXY/HTTPS_PROXY routing and system resolver
	// schemes. Name resolution remains the operator's trusted DNS responsibility.
	cc, err := grpc.NewClient("passthrough:///"+target, grpc.WithTransportCredentials(credentials.NewTLS(config)), grpc.WithContextDialer(dial), grpc.WithDisableRetry(), grpc.WithDefaultCallOptions(grpc.ForceCodec(codec{}), grpc.MaxCallRecvMsgSize(protocol.MaxFrameBytes), grpc.MaxCallSendMsgSize(protocol.MaxFrameBytes)), grpc.WithStaticStreamWindowSize(64<<10), grpc.WithStaticConnWindowSize(1<<20), grpc.WithMaxHeaderListSize(16<<10), grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: false}))
	if err != nil {
		return nil, ErrTransport
	}
	streamCtx, cancel := context.WithDeadline(ctx, leaf.NotAfter)
	failed := true
	defer func() {
		if failed {
			cancel()
			_ = cc.Close()
		}
	}()
	hctx, hcancel := context.WithTimeout(streamCtx, 5*time.Second)
	defer hcancel()
	stream, err := bounded(hctx, 5*time.Second, func() (grpc.BidiStreamingClient[agentdv1.Envelope, agentdv1.Envelope], error) {
		return agentdv1.NewAgentTransportClient(cc).Connect(streamCtx)
	})
	if err != nil {
		return nil, ErrTransport
	}
	hello := proto.Clone(c.Hello).(*agentdv1.Hello)
	if _, err = bounded(hctx, 5*time.Second, func() (struct{}, error) {
		return struct{}{}, stream.Send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Hello{Hello: hello}})
	}); err != nil {
		return nil, ErrTransport
	}
	first, err := bounded(hctx, 5*time.Second, stream.Recv)
	if err != nil || first == nil || !protocol.KnownFields(first) || first.GetWelcome() == nil || !proto.Equal(first, &agentdv1.Envelope{Body: &agentdv1.Envelope_Welcome{Welcome: first.GetWelcome()}}) || protocol.VerifyWelcome(c.Hello, c.Expected, first.GetWelcome()) != nil {
		return nil, ErrTransport
	}
	peerMu.Lock()
	expiry := peerExpiry
	peerMu.Unlock()
	if !expiry.After(time.Now()) {
		return nil, ErrTransport
	}
	sessionCtx, sessionCancel := context.WithDeadline(streamCtx, expiry)
	session := &Session{sendRate: rate.NewLimiter(64, 64), recvRate: rate.NewLimiter(64, 64), ctx: sessionCtx, cancel: func() { sessionCancel(); cancel() }, stream: stream, welcome: proto.Clone(first.GetWelcome()).(*agentdv1.Welcome), check: c.Check, close: func() { _ = cc.Close() }}
	// Closing the RPC on either certificate's expiry unblocks pending gRPC I/O.
	session.watch()
	failed = false
	return session, nil
}
