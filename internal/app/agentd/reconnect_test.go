package agentd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

type reconnectAuthorizer func(context.Context, transport.Peer, []byte) (transport.Lease, error)

func (f reconnectAuthorizer) Admit(ctx context.Context, p transport.Peer, proof []byte) (transport.Lease, error) {
	return f(ctx, p, proof)
}

func reconnectServerCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw, der}, PrivateKey: leafKey}, roots
}

func TestAuthenticatedRotationAndReconnectLoop(t *testing.T) {
	files := transportFixture(t)
	b, err := decodeTransport(files)
	if err != nil {
		t.Fatal(err)
	}
	renewed := transportFixtureTTL(t, 15*time.Minute)
	pair, err := tls.X509KeyPair(renewed["client.crt"], renewed["client.key"])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	for _, cert := range []tls.Certificate{b.certificate, pair} {
		ca, err := x509.ParseCertificate(cert.Certificate[len(cert.Certificate)-1])
		if err != nil {
			t.Fatal(err)
		}
		clientRoots.AddCert(ca)
	}
	serverCert, serverRoots := reconnectServerCertificate(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	b.roots = serverRoots
	b.config.ServerName = "localhost"
	b.config.Endpoint = "https://localhost:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	b.config.Capabilities = append(b.config.Capabilities, "rotation.v1")
	slices.Sort(b.config.Capabilities)
	cfg, err := b.ClientConfig(denyFrame)
	if err != nil {
		t.Fatal(err)
	}
	var epoch atomic.Uint64
	var disconnected atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	makeFrame := func(w *agentdv1.Welcome) *agentdv1.Envelope {
		mid, _ := primitives.NewID(time.Now())
		return &agentdv1.Envelope{Major: w.Major, Minor: w.Minor, ConnectionId: w.ConnectionId, ConnectionEpoch: w.ConnectionEpoch, Binding: proto.Clone(w.Binding).(*agentdv1.Binding), MessageId: string(mid), Sequence: 1}
	}
	serverConfig := grpctransport.ServerConfig{Certificate: serverCert, ServerName: "localhost", TrustDomain: b.domain, ClientRoots: clientRoots, MaxConnections: 8}
	serverConfig.Authorizer = reconnectAuthorizer(func(_ context.Context, p transport.Peer, proof []byte) (transport.Lease, error) {
		n := epoch.Add(1)
		if n == 1 && len(proof) != 32 || n > 1 && len(proof) != 0 {
			return transport.Lease{}, ErrConfig
		}
		cid, _ := primitives.NewID(time.Now())
		return transport.Lease{Expected: cfg.Expected, ConnectionID: cid, Epoch: n, Deadline: time.Now().Add(time.Minute), Close: func() {}, Check: func(_ context.Context, f *agentdv1.Envelope) error {
			if f.ConnectionEpoch != epoch.Load() {
				return ErrConfig
			}
			return nil
		}}, nil
	})
	serverConfig.Handle = func(ctx context.Context, s *grpctransport.Session) error {
		f, err := s.Recv()
		if err != nil {
			return err
		}
		reply := makeFrame(s.Welcome())
		if s.Welcome().ConnectionEpoch == 1 {
			if f.GetRotation() == nil || f.GetRotation().Kind != agentdv1.Rotation_REQUEST {
				return ErrConfig
			}
			reply.Body = &agentdv1.Envelope_Rotation{Rotation: &agentdv1.Rotation{Kind: agentdv1.Rotation_ISSUED, CertificatePem: renewed["client.crt"], PrivateKeyPem: renewed["client.key"], ExpiresUnixMs: leaf.NotAfter.UnixMilli()}}
		} else {
			if f.GetHeartbeat() == nil {
				return ErrConfig
			}
			reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId}}
		}
		if err := s.Send(reply); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	server, err := grpctransport.NewServer(serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = server.Serve(serverCtx, l) }()
	h := ConnectionHooks{Check: func(context.Context, *agentdv1.Envelope) error { return nil }, Disconnected: func(context.Context) error { disconnected.Add(1); return nil }}
	h.Serve = func(ctx context.Context, s *grpctransport.Session, b *TransportBootstrap, renew <-chan time.Time) error {
		f := makeFrame(s.Welcome())
		if s.Welcome().ConnectionEpoch == 1 {
			select {
			case <-renew:
			case <-ctx.Done():
				return ErrConfig
			}
			f.Body = &agentdv1.Envelope_Rotation{Rotation: &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST}}
			if err := s.Send(f); err != nil {
				return err
			}
			reply, err := s.Recv()
			if err != nil {
				return err
			}
			// A wrong epoch must not install even otherwise valid key material.
			for _, change := range []func(*agentdv1.Envelope){
				func(f *agentdv1.Envelope) { f.ConnectionEpoch++ },
				func(f *agentdv1.Envelope) { f.Binding.AttemptId = "01950000-0000-7000-8000-000000000002" },
				func(f *agentdv1.Envelope) { f.GetRotation().ExpiresUnixMs++ },
				func(f *agentdv1.Envelope) { f.GetRotation().PrivateKeyPem = files["client.key"] },
				func(f *agentdv1.Envelope) {
					f.GetRotation().CertificatePem = files["client.crt"]
					f.GetRotation().PrivateKeyPem = files["client.key"]
					old, _ := x509.ParseCertificate(b.certificate.Certificate[0])
					f.GetRotation().ExpiresUnixMs = old.NotAfter.UnixMilli()
				},
			} {
				bad := proto.Clone(reply).(*agentdv1.Envelope)
				change(bad)
				if b.InstallRotation(s, bad) != ErrConfig {
					t.Error("invalid rotation installed")
				}
			}
			if err := b.InstallRotation(s, reply); err != nil {
				t.Error("rotation rejected")
				return err
			}
			clear(reply.GetRotation().PrivateKeyPem)
			if len(b.proof) != 0 || !b.RotationAt().After(time.Now()) {
				t.Error("rotation state not replaced")
			}
			return nil
		}
		f.Body = &agentdv1.Envelope_Heartbeat{Heartbeat: &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_ABSENT}}
		if err := s.Send(f); err != nil {
			return err
		}
		if _, err := s.Recv(); err != nil {
			return err
		}
		cancel()
		return nil
	}
	if err := RunConnections(ctx, b, h); err != nil {
		t.Fatal(err)
	}
	if epoch.Load() != 2 || disconnected.Load() != 2 {
		t.Fatal("rotation did not reconnect exactly once")
	}
	if len(b.certificate.Certificate) != 0 {
		t.Fatal("transport credential retained after loop")
	}
}

func TestExpiredIdentityUsesOnlyFreshMatchingRecovery(t *testing.T) {
	old, err := decodeTransport(transportFixtureTTL(t, 2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	next, err := decodeTransport(transportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	recovered, stopped, dials := 0, 0, 0
	h := ConnectionHooks{Check: denyFrame, Serve: func(context.Context, *grpctransport.Session, *TransportBootstrap, <-chan time.Time) error {
		t.Error("unexpected stream")
		return ErrConfig
	}, Disconnected: func(context.Context) error { stopped++; return nil }, Recovery: func(context.Context) (*TransportBootstrap, error) {
		if stopped != 1 {
			t.Error("recovery preceded local fencing")
		}
		recovered++
		return next, nil
	}}
	err = runConnections(ctx, old, h, func(_ context.Context, c grpctransport.ClientConfig) (*grpctransport.Session, error) {
		dials++
		if len(c.Hello.BootstrapProof) != 32 {
			t.Error("recovery lacked fresh proof")
		}
		cancel()
		return nil, grpctransport.ErrTransport
	})
	if err != nil || recovered != 1 || dials != 1 || stopped != 2 {
		t.Fatal("recovery path failed", err)
	}
}

func TestAmbiguousHandshakeNeverReplaysBootstrap(t *testing.T) {
	b, err := decodeTransport(transportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	attempts := 0
	h := ConnectionHooks{Check: denyFrame, Serve: func(context.Context, *grpctransport.Session, *TransportBootstrap, <-chan time.Time) error {
		return ErrConfig
	}, Disconnected: func(context.Context) error { return nil }}
	err = runConnections(ctx, b, h, func(_ context.Context, c grpctransport.ClientConfig) (*grpctransport.Session, error) {
		attempts++
		if attempts == 1 && len(c.Hello.BootstrapProof) != 32 || attempts > 1 && len(c.Hello.BootstrapProof) != 0 {
			t.Error("proof replay")
		}
		if attempts == 2 {
			cancel()
		}
		return nil, grpctransport.ErrTransport
	})
	if err != nil || attempts != 2 {
		t.Fatal("retry failed", err)
	}
}
