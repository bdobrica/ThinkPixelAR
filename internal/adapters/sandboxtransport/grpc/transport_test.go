package grpctransport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

const id = "01950000-0000-7000-8000-000000000001"
const otherID = "01950000-0000-7000-8000-000000000002"
const testDomain = "agentd.example.invalid"
const testServer = "ar.example.invalid"

type issuer struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	roots *x509.CertPool
}

func newIssuer(t *testing.T) issuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ephemeral test issuer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c)
	return issuer{c, key, roots}
}
func (i issuer) issue(t *testing.T, server bool, edit func(*x509.Certificate)) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Second), NotAfter: time.Now().Add(9 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		c.DNSNames = []string{testServer}
	} else {
		u, _ := url.Parse("spiffe://" + testDomain + "/tenant/" + id + "/sandbox/" + id + "/attempt/" + id)
		c.URIs = []*url.URL{u}
	}
	if edit != nil {
		edit(c)
	}
	der, err := x509.CreateCertificate(rand.Reader, c, i.cert, &key.PublicKey, i.key)
	if err != nil {
		t.Fatal(err)
	}
	c, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, i.cert.Raw}, PrivateKey: key, Leaf: c}
}

type admitFunc func(context.Context, transport.Peer, []byte) (transport.Lease, error)

func (f admitFunc) Admit(ctx context.Context, p transport.Peer, proof []byte) (transport.Lease, error) {
	return f(ctx, p, proof)
}
func expectations() protocol.Expected {
	return protocol.Expected{Binding: &agentdv1.Binding{TenantId: id, SessionId: id, ExecutionId: id, AttemptId: id, SandboxBindingId: id, SessionGeneration: 1}, Challenge: bytes.Repeat([]byte{7}, 32), BuildDigest: "sha256:" + strings.Repeat("a", 64), AdapterKind: "test", AdapterDigest: "sha256:" + strings.Repeat("b", 64), SupportedCapabilities: []string{"envelope.v1"}, RequiredCapabilities: []string{"envelope.v1"}, Limits: protocol.HardLimits()}
}
func hello(e protocol.Expected) *agentdv1.Hello {
	return &agentdv1.Hello{Versions: &agentdv1.VersionRange{Major: 1}, Binding: proto.Clone(e.Binding).(*agentdv1.Binding), Challenge: bytes.Clone(e.Challenge), BuildDigest: e.BuildDigest, AdapterKind: e.AdapterKind, AdapterDigest: e.AdapterDigest, SupportedCapabilities: e.SupportedCapabilities, RequiredCapabilities: e.RequiredCapabilities, Limits: e.Limits, BootstrapProof: bytes.Repeat([]byte{8}, 32)}
}
func lease(e protocol.Expected) transport.Lease {
	return transport.Lease{Expected: e, ConnectionID: primitives.ID(otherID), Epoch: 1, Deadline: time.Now().Add(time.Minute), Check: func(context.Context, *agentdv1.Envelope) error { return nil }, Close: func() {}}
}
func serve(t *testing.T, c ServerConfig) (string, context.CancelFunc) {
	t.Helper()
	s, err := NewServer(c)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server leaked")
		}
	})
	return l.Addr().String(), cancel
}
func dialAt(address string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
}
func frame(w *agentdv1.Welcome) *agentdv1.Envelope {
	return &agentdv1.Envelope{Major: w.Major, Minor: w.Minor, ConnectionId: w.ConnectionId, ConnectionEpoch: w.ConnectionEpoch, MessageId: id, Sequence: 1, Binding: proto.Clone(w.Binding).(*agentdv1.Binding), Body: &agentdv1.Envelope_Heartbeat{Heartbeat: &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_ABSENT}}}
}
func TestRealMTLSBidirectionalStream(t *testing.T) {
	serverCA, clientCA := newIssuer(t), newIssuer(t)
	e := expectations()
	var checks, closes atomic.Int32
	address, _ := serve(t, ServerConfig{Certificate: serverCA.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clientCA.roots, MaxConnections: 8, Authorizer: admitFunc(func(_ context.Context, p transport.Peer, proof []byte) (transport.Lease, error) {
		if string(p.Identity.SandboxID) != id || len(p.CertificateDigest) != 71 || !bytes.Equal(proof, bytes.Repeat([]byte{8}, 32)) {
			return transport.Lease{}, ErrTransport
		}
		l := lease(e)
		l.Check = func(_ context.Context, f *agentdv1.Envelope) error {
			checks.Add(1)
			f.MessageId = "mutated private callback copy"
			return nil
		}
		l.Close = func() { closes.Add(1) }
		return l, nil
	}), Handle: func(ctx context.Context, s *Session) error {
		f, err := s.Recv()
		if err != nil {
			return err
		}
		if f.MessageId != id {
			return ErrTransport
		}
		reply := frame(s.Welcome())
		reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId, AcceptedSequence: 1}}
		if err = s.Send(reply); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}})
	c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: serverCA.roots, Certificate: clientCA.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := connect(ctx, c, dialAt(address))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Send(frame(s.Welcome())); err != nil {
		t.Fatal(err)
	}
	reply, err := s.Recv()
	if err != nil || reply.GetAcknowledgement() == nil {
		t.Fatal("bidirectional exchange failed", err)
	}
	if checks.Load() != 2 {
		t.Fatal("admission not checked per direction")
	}
	s.Close()
	deadline := time.Now().Add(time.Second)
	for closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if closes.Load() != 1 {
		t.Fatal("lease not released exactly once")
	}
}
func TestTLSRejectsInvalidClientsBeforeAdmission(t *testing.T) {
	for _, mode := range []string{"missing", "wrong-ca", "wrong-eku", "expired", "long-lived", "wrong-domain", "extra-san", "wrong-sandbox", "tls12"} {
		t.Run(mode, func(t *testing.T) {
			serverCA, clientCA := newIssuer(t), newIssuer(t)
			e := expectations()
			var admits atomic.Int32
			address, _ := serve(t, ServerConfig{Certificate: serverCA.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clientCA.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
				admits.Add(1)
				return lease(e), nil
			}), Handle: func(context.Context, *Session) error { return nil }})
			edit := func(c *x509.Certificate) {
				switch mode {
				case "wrong-eku":
					c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
				case "expired":
					c.NotBefore = time.Now().Add(-time.Minute)
					c.NotAfter = time.Now().Add(-time.Second)
				case "long-lived":
					c.NotAfter = time.Now().Add(time.Hour)
				case "wrong-domain":
					c.URIs[0].Host = "wrong.invalid"
				case "extra-san":
					c.DNSNames = []string{"extra.invalid"}
				case "wrong-sandbox":
					c.URIs[0].Path = strings.Replace(c.URIs[0].Path, "/sandbox/"+id, "/sandbox/"+otherID, 1)
				}
			}
			issuer := clientCA
			if mode == "wrong-ca" {
				issuer = newIssuer(t)
			}
			tc := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: serverCA.roots, ServerName: testServer, Certificates: []tls.Certificate{issuer.issue(t, false, edit)}}
			if mode == "missing" {
				tc.Certificates = nil
			}
			if mode == "tls12" {
				tc.MinVersion = tls.VersionTLS12
				tc.MaxVersion = tls.VersionTLS12
			}
			cc, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(credentials.NewTLS(tc)), grpc.WithDisableRetry())
			if err != nil {
				t.Fatal(err)
			}
			defer cc.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			stream, err := agentdv1.NewAgentTransportClient(cc).Connect(ctx)
			if err == nil {
				err = stream.Send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Hello{Hello: hello(e)}})
				if err == nil {
					_, err = stream.Recv()
				}
			}
			if err == nil || admits.Load() != 0 {
				t.Fatal("invalid TLS identity reached admission", err, admits.Load())
			}
		})
	}
}
func TestClientRejectsServerIdentityAndTrust(t *testing.T) {
	for _, mode := range []string{"wrong-ca", "wrong-name", "wildcard"} {
		t.Run(mode, func(t *testing.T) {
			ca, clients := newIssuer(t), newIssuer(t)
			e := expectations()
			// A raw gRPC server lets this test present certificates our constructor rejects.
			cert := ca.issue(t, true, func(c *x509.Certificate) {
				if mode == "wrong-name" {
					c.DNSNames = []string{"wrong.invalid"}
				}
				if mode == "wildcard" {
					c.DNSNames = []string{"*.example.invalid"}
				}
			})
			rpc := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clients.roots})))
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = rpc.Serve(l) }()
			defer rpc.Stop()
			roots := ca.roots
			if mode == "wrong-ca" {
				roots = newIssuer(t).roots
			}
			c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if s, err := connect(ctx, c, dialAt(l.Addr().String())); err == nil {
				s.Close()
				t.Fatal("invalid server accepted")
			}
		})
	}
}

func TestAdmissionAndLeaseFailClosed(t *testing.T) {
	for _, mode := range []string{"deny", "wrong-binding", "expired", "missing-check", "bad-proof", "revoke", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ca, clients := newIssuer(t), newIssuer(t)
			e := expectations()
			var delivered, closed atomic.Int32
			address, _ := serve(t, ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(_ context.Context, _ transport.Peer, proof []byte) (transport.Lease, error) {
				if mode == "deny" || !bytes.Equal(proof, bytes.Repeat([]byte{8}, 32)) {
					return transport.Lease{}, ErrTransport
				}
				l := lease(e)
				l.Close = func() { closed.Add(1) }
				switch mode {
				case "wrong-binding":
					l.Expected = expectations()
					l.Expected.Binding.AttemptId = otherID
				case "expired":
					l.Deadline = time.Now().Add(-time.Second)
				case "missing-check":
					l.Check = nil
				case "revoke":
					l.Check = func(context.Context, *agentdv1.Envelope) error { return ErrTransport }
				case "deadline":
					l.Deadline = time.Now().Add(200 * time.Millisecond)
				}
				return l, nil
			}), Handle: func(_ context.Context, s *Session) error {
				_, err := s.Recv()
				if err == nil {
					delivered.Add(1)
				}
				return err
			}})
			c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: ca.roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
			if mode == "bad-proof" {
				c.Hello.BootstrapProof[0]++
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s, err := connect(ctx, c, dialAt(address))
			if mode == "revoke" || mode == "deadline" {
				if err != nil {
					t.Fatal("setup failed", err)
				}
				defer s.Close()
				if mode == "revoke" {
					_ = s.Send(frame(s.Welcome()))
				}
				if _, err = s.Recv(); err == nil {
					t.Fatal("invalid lease delivered a frame")
				}
			} else if err == nil {
				s.Close()
				t.Fatal("invalid admission accepted")
			}
			if delivered.Load() != 0 {
				t.Fatal("frame bypassed authoritative check")
			}
			if mode != "deny" && mode != "bad-proof" && closed.Load() != 1 {
				t.Fatal("lease not closed")
			}
		})
	}
}
func TestOneActiveStreamPerIdentity(t *testing.T) {
	ca, clients := newIssuer(t), newIssuer(t)
	e := expectations()
	var admits atomic.Int32
	address, _ := serve(t, ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
		admits.Add(1)
		return lease(e), nil
	}), Handle: func(ctx context.Context, _ *Session) error { <-ctx.Done(); return nil }})
	c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: ca.roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first, err := connect(ctx, c, dialAt(address))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := connect(ctx, c, dialAt(address)); err == nil {
		second.Close()
		t.Fatal("parallel identity accepted")
	}
	if admits.Load() != 1 {
		t.Fatal("parallel stream consumed admission")
	}
}
func TestFrameBoundsAndClosedKinds(t *testing.T) {
	e := expectations()
	w, err := protocol.Negotiate(hello(e), e, otherID, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{welcome: w}
	for name, change := range map[string]func(*agentdv1.Envelope){
		"epoch":    func(f *agentdv1.Envelope) { f.ConnectionEpoch++ },
		"binding":  func(f *agentdv1.Envelope) { f.Binding.SessionGeneration++ },
		"version":  func(f *agentdv1.Envelope) { f.Major++ },
		"sequence": func(f *agentdv1.Envelope) { f.Sequence = 0 },
		"id":       func(f *agentdv1.Envelope) { f.MessageId = "untrusted" },
		"enum":     func(f *agentdv1.Envelope) { f.GetHeartbeat().ProcessState = 999 },
		"hello":    func(f *agentdv1.Envelope) { f.Body = &agentdv1.Envelope_Hello{Hello: hello(e)} },
		"rotation": func(f *agentdv1.Envelope) {
			f.Body = &agentdv1.Envelope_Rotation{Rotation: &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST}}
		},
		"diagnostic": func(f *agentdv1.Envelope) {
			f.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_DIAGNOSTIC, Payload: make([]byte, 16385)}}
		},
		"payload": func(f *agentdv1.Envelope) {
			f.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_CANDIDATE_EVENT, Payload: make([]byte, (256<<10)+1)}}
		},
		"unknown": func(f *agentdv1.Envelope) { f.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 1}) },
	} {
		t.Run(name, func(t *testing.T) {
			f := frame(w)
			change(f)
			if s.valid(f, false) {
				t.Fatal("invalid frame accepted")
			}
		})
	}
	f := frame(w)
	if !s.valid(f, false) || s.valid(f, true) {
		t.Fatal("frame direction not enforced")
	}
	if (codec{}).Unmarshal(make([]byte, protocol.MaxFrameBytes+1), new(agentdv1.Envelope)) == nil {
		t.Fatal("oversized wire accepted")
	}
	raw := []byte{}
	for i := 0; i < 20; i++ {
		raw = append(raw, 0x0b)
	}
	for i := 0; i < 20; i++ {
		raw = append(raw, 0x0c)
	}
	if (codec{}).Unmarshal(raw, new(agentdv1.Envelope)) == nil {
		t.Fatal("nested wire accepted")
	}
}
func TestMandatoryServerGuards(t *testing.T) {
	ca, clients := newIssuer(t), newIssuer(t)
	c := ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Handle: func(context.Context, *Session) error { return nil }}
	if _, err := NewServer(c); err == nil {
		t.Fatal("missing authorizer accepted")
	}
	c.Authorizer = admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
		return lease(expectations()), nil
	})
	c.ClientRoots = nil
	if _, err := NewServer(c); err == nil {
		t.Fatal("system roots fallback")
	}
}

func TestRejectSharedClientAndServerIssuer(t *testing.T) {
	ca := newIssuer(t)
	c := ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: ca.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
		return lease(expectations()), nil
	}), Handle: func(context.Context, *Session) error { return nil }}
	if _, err := NewServer(c); err == nil {
		t.Fatal("shared issuer accepted")
	}
}

type testWire struct{ sent int }

func (w *testWire) Send(*agentdv1.Envelope) error     { w.sent++; return nil }
func (w *testWire) Recv() (*agentdv1.Envelope, error) { return nil, ErrTransport }
func TestRateExhaustionClosesBeforeWire(t *testing.T) {
	e := expectations()
	welcome, err := protocol.Negotiate(hello(e), e, otherID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &testWire{}
	s := &Session{ctx: ctx, cancel: cancel, stream: wire, welcome: welcome, check: func(context.Context, *agentdv1.Envelope) error { return nil }, sendRate: rate.NewLimiter(0, 2)}
	for i := 0; i < 2; i++ {
		if err = s.Send(frame(welcome)); err != nil {
			t.Fatal(err)
		}
	}
	if s.Send(frame(welcome)) == nil || ctx.Err() == nil || wire.sent != 2 {
		t.Fatal("rate exhaustion did not close before delivery")
	}
}
func TestMutatingCommandsNeedStableOperationAndDeadline(t *testing.T) {
	e := expectations()
	w, err := protocol.Negotiate(hello(e), e, otherID, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{welcome: w}
	f := frame(w)
	f.Body = &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: agentdv1.Command_START}}
	if s.valid(f, true) {
		t.Fatal("unbound mutation accepted")
	}
	f.OperationId = id
	f.RequestDigest = "sha256:" + strings.Repeat("c", 64)
	f.SentUnixMs = time.Now().UnixMilli()
	f.DeadlineUnixMs = time.Now().Add(time.Second).UnixMilli()
	if !s.valid(f, true) || s.valid(f, false) {
		t.Fatal("valid command direction failed")
	}
	f.DeadlineUnixMs = f.SentUnixMs - 1
	if s.valid(f, true) {
		t.Fatal("expired command accepted")
	}
}

func TestQuietPeerClosesWithoutCallerReceive(t *testing.T) {
	ca, clients := newIssuer(t), newIssuer(t)
	e := expectations()
	e.Limits.HeartbeatIntervalMs = 20
	e.Limits.LivenessWindowMs = 100
	address, _ := serve(t, ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) { return lease(e), nil }), Handle: func(ctx context.Context, _ *Session) error { <-ctx.Done(); return nil }})
	c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: ca.roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := connect(ctx, c, dialAt(address))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case <-s.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("idle peer outlived liveness window")
	}
}
