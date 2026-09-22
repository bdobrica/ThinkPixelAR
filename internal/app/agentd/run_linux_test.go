package agentd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdserver"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type hostRotationFixture struct {
	reply *agentdv1.Rotation
	calls atomic.Int32
}

func (r *hostRotationFixture) Rotate(_ context.Context, _ transport.Peer, _ primitives.ID, _ uint64, request *agentdv1.Rotation) (*agentdv1.Rotation, error) {
	if request.Kind != agentdv1.Rotation_REQUEST {
		return nil, errors.New("fixture rotation rejected")
	}
	r.calls.Add(1)
	return &agentdv1.Rotation{Kind: agentdv1.Rotation_ISSUED, CertificatePem: append([]byte(nil), r.reply.CertificatePem...), PrivateKeyPem: append([]byte(nil), r.reply.PrivateKeyPem...), ExpiresUnixMs: r.reply.ExpiresUnixMs}, nil
}

type hostOutcomeFixture struct{ acknowledged atomic.Bool }

func (o *hostOutcomeFixture) CommandOutcome(context.Context, primitives.ID, primitives.ID, primitives.ID, string) (transport.DispatchOutcome, error) {
	if o.acknowledged.Load() {
		return transport.DispatchAcknowledged, nil
	}
	return "", transport.ErrCommandNotFound
}

// This test uses real mTLS, the runnable supervisor, and the AR stream handler.
// Its authorizer is a fixture; PostgreSQL and homelab checks have separate tests.
func TestRunnableSupervisorAuthenticatedRotationAndStatus(t *testing.T) {
	b, err := decodeTransport(transportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Destroy()
	renewed := transportFixtureTTL(t, 15*time.Minute)
	pair, err := tls.X509KeyPair(renewed["client.crt"], renewed["client.key"])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	for _, cert := range []tls.Certificate{b.certificate, pair} {
		ca, e := x509.ParseCertificate(cert.Certificate[1])
		if e != nil {
			t.Fatal(e)
		}
		roots.AddCert(ca)
	}
	serverCert, serverRoots := reconnectServerCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	b.roots = serverRoots
	b.config.ServerName = "localhost"
	b.config.Endpoint = "https://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	b.config.ControlDeadlineUnixMS = time.Now().Add(time.Minute).UnixMilli()
	b.config.Harness.StopGraceMS = 1000
	b.config.Capabilities = append(b.config.Capabilities, control.Capability, "rotation.v1")
	b.config.RequiredCapabilities = append(b.config.RequiredCapabilities, control.Capability, "rotation.v1")
	cfg, err := b.ClientConfig(denyFrame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, _ := primitives.NewID(time.Now())
	handle, _ := primitives.NewID(time.Now())
	digest := ConfigurationDigest(b.config)
	rotation := &hostRotationFixture{reply: &agentdv1.Rotation{CertificatePem: renewed["client.crt"], PrivateKeyPem: renewed["client.key"], ExpiresUnixMs: leaf.NotAfter.UnixMilli()}}
	outcomes := &hostOutcomeFixture{}
	var epoch atomic.Uint64
	var heartbeats atomic.Int32
	completed := make(chan struct{}, 1)
	finalObserved := make(chan struct{}, 1)
	handler := agentdserver.Handler{Rotation: rotation, Outcomes: outcomes,
		Plan: func(context.Context, transport.Peer) ([]agentdserver.Command, error) {
			if epoch.Load() == 1 {
				return nil, nil
			}
			return []agentdserver.Command{{OperationID: string(id), HarnessHandle: string(handle), ConfigurationDigest: digest, Kind: agentdv1.Command_STATUS, Deadline: time.Now().Add(5 * time.Second)}}, nil
		},
		Observe: func(_ context.Context, f *agentdv1.Envelope) error {
			if control.IsShutdownObservation(f) {
				finalObserved <- struct{}{}
			}
			if f.GetHeartbeat() != nil {
				heartbeats.Add(1)
				if outcomes.acknowledged.Load() {
					select {
					case completed <- struct{}{}:
					default:
					}
				}
			}
			return nil
		},
	}
	server, err := grpctransport.NewServer(grpctransport.ServerConfig{Certificate: serverCert, ServerName: "localhost", TrustDomain: b.domain, ClientRoots: roots, MaxConnections: 4, Handle: handler.Serve,
		Authorizer: reconnectAuthorizer(func(_ context.Context, _ transport.Peer, proof []byte) (transport.Lease, error) {
			n := epoch.Add(1)
			if n == 1 && len(proof) != 32 || n > 1 && len(proof) != 0 {
				return transport.Lease{}, ErrConfig
			}
			cid, _ := primitives.NewID(time.Now())
			var inbound, outbound control.Sequences
			return transport.Lease{Expected: cfg.Expected, ConnectionID: cid, Epoch: n, Deadline: time.Now().Add(time.Minute), Close: func() {}, Check: func(_ context.Context, f *agentdv1.Envelope) error {
				if f.ConnectionEpoch != epoch.Load() {
					return ErrConfig
				}
				if f.GetCommand() != nil {
					if control.Command(f, digest) != nil {
						return ErrConfig
					}
					return outbound.Check(f)
				}
				if f.GetRotation() != nil && f.GetRotation().Kind == agentdv1.Rotation_ISSUED || f.GetAcknowledgement() != nil && f.OperationId == "" {
					return outbound.Check(f)
				}
				if err := inbound.Check(f); err != nil {
					return err
				}
				if f.GetAcknowledgement() != nil && f.OperationId == string(id) {
					outcomes.acknowledged.Store(true)
				}
				return nil
			}}, nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx, listener) }()
	clientDone := make(chan error, 1)
	clientCtx, stopClient := context.WithCancel(ctx)
	defer stopClient()
	go func() { clientDone <- RunWithBootstrap(clientCtx, b, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
	select {
	case <-completed:
	case <-ctx.Done():
		t.Error("authenticated supervisor did not complete status")
	}
	started := time.Now()
	stopClient()
	if err := <-clientDone; err != nil {
		t.Error(err)
	}
	if time.Since(started) > 5*time.Second {
		t.Error("shutdown exceeded reporting grace")
	}
	select {
	case <-finalObserved:
	default:
		t.Error("final authenticated observation was not delivered")
	}
	cancel()
	if err := <-serverDone; err != nil {
		t.Error(err)
	}
	if epoch.Load() < 2 || rotation.calls.Load() != 1 || !outcomes.acknowledged.Load() || heartbeats.Load() == 0 {
		t.Fatalf("missing rotation, reconnect, status or heartbeat: epochs=%d rotations=%d ack=%v heartbeats=%d", epoch.Load(), rotation.calls.Load(), outcomes.acknowledged.Load(), heartbeats.Load())
	}
}

func TestRunnableSupervisorRejectsMissingCutoffAndStopBudget(t *testing.T) {
	for _, mode := range []string{"cutoff", "budget", "capabilities"} {
		t.Run(mode, func(t *testing.T) {
			b, err := decodeTransport(transportFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			b.config.ControlDeadlineUnixMS = time.Now().Add(time.Minute).UnixMilli()
			b.config.Harness.StopGraceMS = 1000
			b.config.Capabilities = append(b.config.Capabilities, control.Capability, "rotation.v1")
			b.config.RequiredCapabilities = append(b.config.RequiredCapabilities, control.Capability, "rotation.v1")
			switch mode {
			case "cutoff":
				b.config.ControlDeadlineUnixMS = 0
			case "budget":
				b.config.Harness.StopGraceMS = 5000
			case "capabilities":
				b.config.RequiredCapabilities = nil
			}
			if err := RunWithBootstrap(context.Background(), b, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != ErrConfig {
				t.Fatal(err)
			}
			if len(b.certificate.Certificate) != 0 {
				t.Fatal("rejected bootstrap retained credentials")
			}
		})
	}
}
