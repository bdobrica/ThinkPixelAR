package agentd

import (
	"bytes"
	"context"
	"crypto/x509"
	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"github.com/bdobrica/ThinkPixelAR/test/harnessfixture"
	"google.golang.org/protobuf/proto"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func controlCommand(t *testing.T, kind agentdv1.Command_Kind, handle string) *agentdv1.Envelope {
	t.Helper()
	id, err := primitives.NewID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &agentdv1.Envelope{OperationId: string(id), HarnessHandle: handle, RequestDigest: control.Digest(kind, "test-config", handle), SentUnixMs: time.Now().UnixMilli(), DeadlineUnixMs: time.Now().Add(5 * time.Second).UnixMilli(), Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: kind, ConfigurationDigest: "test-config", PayloadSchema: control.Capability}}}
}
func TestProcessControlReplayAndFencing(t *testing.T) {
	p, _ := processFixture(t, "ignore")
	c := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
	handle, _ := primitives.NewID(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := controlCommand(t, agentdv1.Command_START, string(handle))
	result := c.execute(ctx, f)
	if result.failed || result.status.ProcessID == "" {
		t.Fatal("start failed")
	}
	if replay := c.execute(ctx, f); replay != result {
		t.Fatal("replay re-executed")
	}
	other := controlCommand(t, agentdv1.Command_RESTART, string(handle))
	other.OperationId = f.OperationId
	if !c.execute(ctx, other).failed {
		t.Fatal("conflicting operation accepted")
	}
	if !c.execute(ctx, controlCommand(t, agentdv1.Command_START, string(handle))).failed {
		t.Fatal("second start accepted")
	}
	if !c.execute(ctx, controlCommand(t, agentdv1.Command_STOP, string(f.OperationId))).failed {
		t.Fatal("wrong handle stopped process")
	}
	if err := c.Disconnected(ctx); err != nil {
		t.Fatal(err)
	}
	if replay := c.execute(ctx, f); replay != result {
		t.Fatal("disconnect replay restarted work")
	}
	if p.Status().State != agentdv1.Heartbeat_EXITED {
		t.Fatal("disconnect left work running")
	}
}

func TestAuthenticatedProcessControlExchange(t *testing.T) {
	authenticatedProcessControlExchange(t, false)
}

func TestAuthenticatedCodexThreadExchange(t *testing.T) {
	authenticatedProcessControlExchange(t, true)
}

func TestAuthenticatedCodexTurnExchange(t *testing.T) {
	authenticatedProcessControlExchange(t, true, true)
}

func authenticatedProcessControlExchange(t *testing.T, thread bool, turns ...bool) {
	turn := len(turns) == 1 && turns[0]
	files := transportFixture(t)
	b, err := decodeTransport(files)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Destroy()
	serverCert, serverRoots := reconnectServerCertificate(t)
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
	b.roots = serverRoots
	b.config.ServerName = "localhost"
	b.config.Endpoint = "https://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	b.config.Capabilities = append(b.config.Capabilities, control.Capability)
	p, root := processFixture(t, "ignore")
	socket := filepath.Join(root, "transport.sock")
	p.config.Argv = []string{p.config.Argv[0], "-test.run=^TestStructuredHarnessChild$", "structured-fixture", socket}
	p.captureLimits = protocol.HardLimits()
	p.sanitizer = func(_ OutputSource, raw []byte) ([]byte, error) { return harnessfixture.Sanitize(raw) }
	if thread {
		p.config.Argv = []string{p.config.Argv[0], "-test.run=^TestCodexChild$", "codex-fixture", "thread"}
		p.codex, p.createThread, p.sanitizer = true, true, nil
		b.config.Capabilities = append(b.config.Capabilities, control.ThreadCapability)
		b.config.RequiredCapabilities = append(b.config.RequiredCapabilities, control.ThreadCapability)
	}
	if turn {
		p.config.Argv[len(p.config.Argv)-1] = "turn"
		p.executeTurn = true
		p.commandBytes = b.config.Limits.CommandBytes
		b.config.Capabilities = append(b.config.Capabilities, control.TurnCapability)
		b.config.RequiredCapabilities = append(b.config.RequiredCapabilities, control.TurnCapability)
	}
	adapter := harnessfixture.Adapter{Path: socket}
	ctl := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
	cfg, err := b.ClientConfig(ctl.CheckClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	handle, _ := primitives.NewID(time.Now())
	server, err := grpctransport.NewServer(grpctransport.ServerConfig{Certificate: serverCert, ServerName: "localhost", TrustDomain: b.domain, ClientRoots: clientRoots, MaxConnections: 1,
		Authorizer: reconnectAuthorizer(func(_ context.Context, peer transport.Peer, proof []byte) (transport.Lease, error) {
			if peer.Identity.TenantID != primitives.ID(b.config.Binding.TenantId) || !bytes.Equal(proof, b.proof) {
				return transport.Lease{}, control.ErrControl
			}
			cid, _ := primitives.NewID(time.Now())
			var inbound, outbound control.Sequences
			return transport.Lease{Expected: cfg.Expected, ConnectionID: cid, Epoch: 1, Deadline: time.Now().Add(time.Minute), Close: func() {}, Check: func(_ context.Context, f *agentdv1.Envelope) error {
				if f.GetCommand() != nil {
					if control.Command(f, "test-config") != nil {
						return control.ErrControl
					}
					return outbound.Check(f)
				}
				if f.GetAcknowledgement() == nil && f.GetHeartbeat() == nil && f.GetFailure() == nil && f.GetObservation() == nil {
					return control.ErrControl
				}
				return inbound.Check(f)
			}}, nil
		}), Handle: func(_ context.Context, s *grpctransport.Session) error {
			sequence := uint64(0)
			send := func(f *agentdv1.Envelope) error {
				sequence++
				w := s.Welcome()
				mid, _ := primitives.NewID(time.Now())
				f.Sequence = sequence
				f.MessageId = string(mid)
				f.ConnectionId = w.ConnectionId
				f.ConnectionEpoch = w.ConnectionEpoch
				f.Binding = w.Binding
				f.Major = w.Major
				f.Minor = w.Minor
				return s.Send(f)
			}
			ready := false
			start := controlCommand(t, agentdv1.Command_START, string(handle))
			commands := []*agentdv1.Envelope{start, proto.Clone(start).(*agentdv1.Envelope), controlCommand(t, agentdv1.Command_STATUS, string(handle)), controlCommand(t, agentdv1.Command_INTERRUPT, string(handle))}
			if turn {
				execute := turnCommand(t, string(handle))
				commands = []*agentdv1.Envelope{start, execute, proto.Clone(execute).(*agentdv1.Envelope), controlCommand(t, agentdv1.Command_INTERRUPT, string(handle))}
			}
			for _, command := range commands {
				if err := send(command); err != nil {
					finished <- err
					return err
				}
				acknowledged := false
				for !acknowledged || !ready {
					response, err := s.Recv()
					if err != nil {
						finished <- err
						return err
					}
					if o := response.GetObservation(); o != nil {
						if thread {
							if o.Kind == agentdv1.Observation_DIAGNOSTIC && o.PayloadSchema == control.Capability+"/stderr" && string(o.Payload) == "[REDACTED]" {
								continue
							}
							v, e := control.ThreadObservation(response)
							if e != nil || v.ThreadID != "01950000-0000-7000-8000-000000000099" || response.OperationId != start.OperationId || response.RequestDigest != start.RequestDigest {
								finished <- control.ErrControl
								return control.ErrControl
							}
							ready = true
							continue
						}
						if o.Kind != agentdv1.Observation_DIAGNOSTIC || o.PayloadSchema != control.Capability+"/stdout" || string(o.Payload) != "fixture.v1 ready" {
							finished <- control.ErrControl
							return control.ErrControl
						}
						ready = true
						if err := adapter.Expect(ctx, "hello fixture.v1", "fixture.v1"); err != nil {
							finished <- err
							return err
						}
						continue
					}
					if response.GetHeartbeat() != nil {
						continue
					}
					ack := response.GetAcknowledgement()
					if thread && !ready {
						finished <- control.ErrControl
						return control.ErrControl
					}
					if ack == nil || response.OperationId != command.OperationId || ack.RequestDigest != command.RequestDigest || ack.AcceptedSequence != command.Sequence {
						finished <- control.ErrControl
						return control.ErrControl
					}
					acknowledged = true
				}
			}
			finished <- nil
			<-ctx.Done()
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx, listener) }()
	defer func() { cancel(); <-serverDone }()
	session, err := grpctransport.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	clientDone := make(chan error, 1)
	go func() { clientDone <- ctl.Serve(ctx, session, b, nil) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal("authenticated command exchange", err)
		}
	case <-ctx.Done():
		t.Fatal("exchange timed out")
	}
	cancel()
	session.Close()
	<-clientDone
	if err := ctl.Disconnected(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.Status().State != agentdv1.Heartbeat_EXITED {
		t.Fatal("interrupt did not stop controlled process")
	}
	if len(ctl.operations) != 3 {
		t.Fatal("duplicate mutation was re-executed")
	}
}

func turnCommand(t *testing.T, handle string) *agentdv1.Envelope {
	t.Helper()
	f := controlCommand(t, agentdv1.Command_EXECUTE, handle)
	f.GetCommand().PayloadSchema = control.TurnCapability
	f.GetCommand().Payload = []byte(`{"input_id":"01950000-0000-7000-8000-000000000088","classification":"Confidential","text":"execution prompt"}`)
	f.RequestDigest = control.TurnDigest(f.GetCommand().ConfigurationDigest, handle, f.GetCommand().Payload)
	return f
}
