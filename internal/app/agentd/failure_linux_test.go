package agentd

import (
	"context"
	"crypto/x509"
	"net"
	"strconv"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdserver"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestConnectionFailureStopsRealProcess(t *testing.T) {
	for _, mode := range []string{"disconnect", "signal"} {
		t.Run(mode, func(t *testing.T) {
			b, err := decodeTransport(transportFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Destroy()
			cert, roots := reconnectServerCertificate(t)
			clientRoots := x509.NewCertPool()
			ca, err := x509.ParseCertificate(b.certificate.Certificate[1])
			if err != nil {
				t.Fatal(err)
			}
			clientRoots.AddCert(ca)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			b.roots = roots
			b.config.ServerName = "localhost"
			b.config.Endpoint = "https://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
			b.config.Capabilities = append(b.config.Capabilities, control.Capability, "rotation.v1")
			b.sessionCredential = true // Test this connection's loss, independently of renewal.
			p, _ := processFixture(t, "ignore")
			ctl := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err = p.Start(ctx); err != nil {
				t.Fatal(err)
			}
			cfg, err := b.ClientConfig(ctl.CheckClient)
			if err != nil {
				t.Fatal(err)
			}
			connected := make(chan *grpctransport.Session, 1)
			final := make(chan bool, 1)
			handler := agentdserver.Handler{Rotation: &hostRotationFixture{}, Outcomes: &hostOutcomeFixture{}, Plan: func(context.Context, transport.Peer) ([]agentdserver.Command, error) { return nil, nil }, Observe: func(_ context.Context, f *agentdv1.Envelope) error {
				if control.IsShutdownObservation(f) {
					final <- string(f.GetObservation().Payload) == control.ShutdownStopped && p.Status().State == agentdv1.Heartbeat_EXITED
				}
				return nil
			}}
			server, err := grpctransport.NewServer(grpctransport.ServerConfig{Certificate: cert, ServerName: "localhost", TrustDomain: b.domain, ClientRoots: clientRoots, MaxConnections: 1, Authorizer: reconnectAuthorizer(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
				cid, _ := primitives.NewID(time.Now())
				return transport.Lease{Expected: cfg.Expected, ConnectionID: cid, Epoch: 1, Deadline: time.Now().Add(time.Minute), Close: func() {}, Check: func(context.Context, *agentdv1.Envelope) error { return nil }}, nil
			}), Handle: func(c context.Context, s *grpctransport.Session) error { connected <- s; return handler.Serve(c, s) }})
			if err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() { serverDone <- server.Serve(ctx, listener) }()
			defer func() { cancel(); <-serverDone }()
			clientCtx, stopClient := context.WithCancel(ctx)
			defer stopClient()
			ctl.supervisor = clientCtx
			stopped := make(chan struct{}, 1)
			serving := make(chan struct{}, 1)
			clientDone := make(chan error, 1)
			go func() {
				clientDone <- RunConnections(clientCtx, b, ConnectionHooks{Check: ctl.CheckClient, Serve: func(c context.Context, s *grpctransport.Session, b *TransportBootstrap, renew <-chan time.Time) error {
					serving <- struct{}{}
					return ctl.Serve(c, s, b, renew)
				}, Disconnected: func(c context.Context) error {
					var err error
					if clientCtx.Err() == nil {
						err = ctl.Disconnected(c)
					}
					select {
					case stopped <- struct{}{}:
					default:
					}
					return err
				}})
			}()
			var session *grpctransport.Session
			select {
			case session = <-connected:
			case <-ctx.Done():
				t.Fatal("not connected")
			}
			select {
			case <-serving:
			case <-ctx.Done():
				t.Fatal("client did not establish stream")
			}
			started := time.Now()
			if mode == "signal" {
				stopClient()
			} else {
				session.Close()
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("disconnect failed to stop within five seconds")
			}
			stopClient()
			if err = <-clientDone; err != nil {
				t.Fatal(err)
			}
			if p.Status().State != agentdv1.Heartbeat_EXITED || time.Since(started) > 5*time.Second {
				t.Fatal("work survived disconnect budget")
			}
			if mode == "signal" {
				select {
				case ok := <-final:
					if !ok {
						t.Fatal("reported stop before process exited")
					}
				default:
					t.Fatal("missing final report")
				}
			}
			if err = p.Shutdown(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
