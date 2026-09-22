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
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type ServerConfig struct {
	Certificate             tls.Certificate
	ServerName, TrustDomain string
	ClientRoots             *x509.CertPool // Dedicated client issuer; never system roots.
	Authorizer              transport.Authorizer
	Handle                  func(context.Context, *Session) error // Trusted handler must honor cancellation.
	MaxConnections          int                                   // 1..128, including TLS/Hello pending connections.
}
type Server struct {
	agentdv1.UnimplementedAgentTransportServer
	rpc    *grpc.Server
	config ServerConfig
	mu     sync.Mutex
	active map[transport.Identity]*Session
}

func NewServer(c ServerConfig) (*Server, error) {
	cert, leaf, err := certificate(c.Certificate)
	if err != nil || !dnsName(c.ServerName) || !dnsName(c.TrustDomain) || !serverLeaf(leaf, c.ServerName, time.Now()) || c.ClientRoots == nil || crossTrusted(cert, c.ClientRoots, x509.ExtKeyUsageServerAuth) || c.Authorizer == nil || c.Handle == nil || c.MaxConnections < 1 || c.MaxConnections > 128 {
		return nil, ErrTransport
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: c.ClientRoots.Clone(), ClientAuth: tls.RequireAndVerifyClientCert, SessionTicketsDisabled: true, NextProtos: []string{"h2"}}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "h2" || !strongChains(state.VerifiedChains) || state.ServerName != c.ServerName || len(state.PeerCertificates) == 0 {
			return ErrTransport
		}
		_, err := clientPeer(state.PeerCertificates[0], c.TrustDomain, time.Now())
		return err
	}
	c.Certificate = cert
	s := &Server{config: c, active: map[transport.Identity]*Session{}}
	s.rpc = grpc.NewServer(grpc.ForceServerCodec(codec{}), grpc.Creds(credentials.NewTLS(config)), grpc.MaxRecvMsgSize(protocol.MaxFrameBytes), grpc.MaxSendMsgSize(protocol.MaxFrameBytes), grpc.MaxConcurrentStreams(1), grpc.ConnectionTimeout(5*time.Second), grpc.StaticStreamWindowSize(64<<10), grpc.StaticConnWindowSize(1<<20), grpc.MaxHeaderListSize(16<<10), grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, MaxConnectionIdle: 30 * time.Second, MaxConnectionAge: 15 * time.Minute, MaxConnectionAgeGrace: time.Second}), grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 30 * time.Second, PermitWithoutStream: false}))
	agentdv1.RegisterAgentTransportServer(s.rpc, s)
	return s, nil
}

// Serve uses a dedicated caller-owned listener. Cancellation force-closes streams;
// there is no plaintext server, public HTTP handler, reflection or health service.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	if l == nil {
		return ErrTransport
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.rpc.Stop()
		case <-done:
		}
	}()
	err := s.rpc.Serve(&limitListener{Listener: l, slots: make(chan struct{}, s.config.MaxConnections), rate: rate.NewLimiter(rate.Limit(s.config.MaxConnections), s.config.MaxConnections)})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return ErrTransport
	}
	return nil
}
func (s *Server) Connect(stream grpc.BidiStreamingServer[agentdv1.Envelope, agentdv1.Envelope]) error {
	reject := status.Error(codes.PermissionDenied, "agentd transport rejected")
	p, ok := peer.FromContext(stream.Context())
	if !ok {
		return reject
	}
	auth, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(auth.State.PeerCertificates) == 0 || len(auth.State.VerifiedChains) == 0 {
		return reject
	}
	identity, err := clientPeer(auth.State.PeerCertificates[0], s.config.TrustDomain, time.Now())
	if err != nil {
		return reject
	}
	hctx, hcancel := context.WithTimeout(stream.Context(), 5*time.Second)
	defer hcancel()
	first, err := bounded(hctx, 5*time.Second, stream.Recv)
	if err != nil || first == nil || !protocol.KnownFields(first) || first.GetHello() == nil {
		return reject
	}
	// Handshake frames carry only the typed body, never competing header identity.
	clean := &agentdv1.Envelope{Body: &agentdv1.Envelope_Hello{Hello: first.GetHello()}}
	if !proto.Equal(clean, first) {
		return reject
	}
	hello := first.GetHello()
	if hello.Binding == nil || hello.Binding.TenantId != string(identity.Identity.TenantID) || hello.Binding.SandboxBindingId != string(identity.Identity.SandboxID) || hello.Binding.AttemptId != string(identity.Identity.AttemptID) || (len(hello.BootstrapProof) != 0 && len(hello.BootstrapProof) != 32) {
		return reject
	}
	lease, err := s.config.Authorizer.Admit(hctx, identity, append([]byte(nil), hello.BootstrapProof...))
	if err != nil {
		return reject
	}
	if lease.Close == nil {
		return reject
	}
	defer lease.Close()
	b := lease.Expected.Binding
	if b == nil || b.TenantId != string(identity.Identity.TenantID) || b.SandboxBindingId != string(identity.Identity.SandboxID) || b.AttemptId != string(identity.Identity.AttemptID) || lease.Check == nil || !lease.Deadline.After(time.Now()) || lease.Deadline.After(identity.ExpiresAt) {
		return reject
	}
	welcome, err := protocol.Negotiate(hello, lease.Expected, string(lease.ConnectionID), lease.Epoch)
	if err != nil {
		return reject
	}
	deadline := lease.Deadline
	serverLeaf, parseErr := x509.ParseCertificate(s.config.Certificate.Certificate[0])
	if parseErr != nil {
		return reject
	}
	if serverLeaf.NotAfter.Before(deadline) {
		deadline = serverLeaf.NotAfter
	}
	ctx, cancel := context.WithDeadline(stream.Context(), deadline)
	defer cancel()
	session := &Session{sendRate: rate.NewLimiter(64, 64), recvRate: rate.NewLimiter(64, 64), ctx: ctx, cancel: cancel, stream: stream, welcome: welcome, server: true, check: lease.Check, peer: identity}
	defer session.Close()
	s.mu.Lock()
	previous := s.active[identity.Identity]
	if previous != nil && previous.welcome.ConnectionEpoch >= lease.Epoch {
		s.mu.Unlock()
		return reject
	}
	s.active[identity.Identity] = session
	s.mu.Unlock()
	if previous != nil {
		previous.Close()
	}
	defer func() {
		s.mu.Lock()
		if s.active[identity.Identity] == session {
			delete(s.active, identity.Identity)
		}
		s.mu.Unlock()
	}()
	if _, err = bounded(hctx, 5*time.Second, func() (struct{}, error) {
		return struct{}{}, stream.Send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Welcome{Welcome: welcome}})
	}); err != nil {
		return reject
	}
	session.watch()
	_, err = bounded(ctx, time.Until(deadline), func() (struct{}, error) { return struct{}{}, s.config.Handle(ctx, session) })
	if err != nil {
		return status.Error(codes.Unavailable, "agentd stream closed")
	}
	return nil
}

type limitListener struct {
	net.Listener
	slots chan struct{}
	rate  *rate.Limiter
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if !l.rate.Allow() {
			_ = c.Close()
			continue
		}
		select {
		case l.slots <- struct{}{}:
			return &limitConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
