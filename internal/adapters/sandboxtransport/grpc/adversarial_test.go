package grpctransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

// wireFixtureCodec deliberately bypasses outgoing application validation. It is
// local to adversarial test clients, never registered as a production codec.
type wireFixtureCodec struct{}

func (wireFixtureCodec) Name() string { return "proto" }
func (wireFixtureCodec) Marshal(v any) ([]byte, error) {
	if raw, ok := v.([]byte); ok {
		return raw, nil
	}
	return proto.Marshal(v.(proto.Message))
}
func (wireFixtureCodec) Unmarshal(raw []byte, v any) error {
	return proto.Unmarshal(raw, v.(proto.Message))
}

func TestHostileWireFramesRejectedBeforeDelivery(t *testing.T) {
	for _, mode := range []string{"truncated", "invalid-wire-type", "unknown-envelope", "unknown-nested", "missing-body", "unknown-enum", "wrong-direction", "old-epoch", "future-epoch", "zero-sequence", "unnegotiated-rotation", "oversized-event", "oversized-diagnostic", "oversized-frame", "event-at-limit"} {
		t.Run(mode, func(t *testing.T) {
			servers, clients := newIssuer(t), newIssuer(t)
			e := expectations()
			e.Limits.EventBytes = 1024
			e.Limits.DiagnosticBytes = 256
			var checked atomic.Int32
			delivered := make(chan error, 1)
			address, _ := serve(t, ServerConfig{Certificate: servers.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
				l := lease(e)
				l.Epoch = 2
				l.Check = func(_ context.Context, f *agentdv1.Envelope) error {
					if f.GetAcknowledgement() == nil {
						checked.Add(1)
					}
					return nil
				}
				return l, nil
			}), Handle: func(_ context.Context, s *Session) error {
				f, err := s.Recv()
				if err == nil {
					reply := frame(s.Welcome())
					reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId}}
					err = s.Send(reply)
				}
				delivered <- err
				return err
			}})
			tc := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, RootCAs: servers.roots, ServerName: testServer, Certificates: []tls.Certificate{clients.issue(t, false, nil)}}
			cc, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(credentials.NewTLS(tc)), grpc.WithDisableRetry(), grpc.WithDefaultCallOptions(grpc.ForceCodec(wireFixtureCodec{})))
			if err != nil {
				t.Fatal(err)
			}
			defer cc.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := agentdv1.NewAgentTransportClient(cc).Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Hello{Hello: hello(e)}}); err != nil {
				t.Fatal(err)
			}
			welcome, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			f := frame(welcome.GetWelcome())
			switch mode {
			case "unknown-envelope":
				f.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 1})
			case "unknown-nested":
				f.GetHeartbeat().ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 1})
			case "missing-body":
				f.Body = nil
			case "unknown-enum":
				f.GetHeartbeat().ProcessState = 999
			case "wrong-direction":
				f.Body = &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: agentdv1.Command_STATUS}}
			case "old-epoch":
				f.ConnectionEpoch--
			case "future-epoch":
				f.ConnectionEpoch++
			case "zero-sequence":
				f.Sequence = 0
			case "unnegotiated-rotation":
				f.Body = &agentdv1.Envelope_Rotation{Rotation: &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST}}
			case "oversized-event", "event-at-limit":
				size := 1024
				if mode == "oversized-event" {
					size++
				}
				f.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_CANDIDATE_EVENT, Payload: bytes.Repeat([]byte{'x'}, size)}}
			case "oversized-diagnostic":
				f.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_DIAGNOSTIC, Payload: bytes.Repeat([]byte{'x'}, 257)}}
			}
			raw, err := proto.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "truncated":
				raw = []byte{0x3a, 0x7f, 1}
			case "invalid-wire-type":
				raw = []byte{0x0f}
			case "oversized-frame":
				raw = make([]byte, protocol.MaxFrameBytes+1)
			}
			// Send may race the server closing an invalid stream; Recv is authoritative.
			_ = stream.SendMsg(raw)
			reply, recvErr := stream.Recv()
			select {
			case err := <-delivered:
				if mode == "event-at-limit" {
					if err != nil || recvErr != nil || reply.GetAcknowledgement() == nil || checked.Load() != 1 {
						t.Fatal("valid boundary rejected")
					}
				} else if err == nil || recvErr == nil || checked.Load() != 0 {
					t.Fatal("hostile frame reached application policy")
				}
			case <-ctx.Done():
				t.Fatal("hostile frame did not close within bound")
			}
		})
	}
}

func TestReplayPolicyControlsAuthenticatedDelivery(t *testing.T) {
	// The policy below is a deliberately explicit test fixture. Production durable
	// idempotency and identical-replay acknowledgement remain AGD-020. This proves
	// no replay denied by that policy can reach the receiving application.
	for _, mode := range []string{"reused-sequence", "sequence-gap", "conflicting-message-id", "identical-replay"} {
		t.Run(mode, func(t *testing.T) {
			servers, clients := newIssuer(t), newIssuer(t)
			e := expectations()
			var received, policyCalls atomic.Int32
			done := make(chan struct{})
			address, _ := serve(t, ServerConfig{Certificate: servers.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
				l := lease(e)
				var previous *agentdv1.Envelope
				l.Check = func(_ context.Context, f *agentdv1.Envelope) error {
					if f.GetAcknowledgement() != nil {
						return nil
					}
					policyCalls.Add(1)
					if previous != nil && mode == "identical-replay" && proto.Equal(f, previous) {
						return nil
					}
					if previous != nil && (f.Sequence != previous.Sequence+1 || f.MessageId == previous.MessageId && !proto.Equal(f, previous)) {
						return ErrTransport
					}
					previous = proto.Clone(f).(*agentdv1.Envelope)
					return nil
				}
				return l, nil
			}), Handle: func(_ context.Context, s *Session) error {
				defer close(done)
				for {
					f, err := s.Recv()
					if err != nil {
						return err
					}
					received.Add(1)
					reply := frame(s.Welcome())
					reply.Sequence = f.Sequence
					reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId, AcceptedSequence: f.Sequence}}
					if err := s.Send(reply); err != nil {
						return err
					}
				}
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: servers.roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
			s, err := connect(ctx, c, dialAt(address))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			f := frame(s.Welcome())
			if err := s.Send(f); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Recv(); err != nil {
				t.Fatal("first frame rejected")
			}
			switch mode {
			case "reused-sequence":
				f.MessageId = otherID
			case "sequence-gap":
				f.Sequence = 3
				f.MessageId = otherID
			case "conflicting-message-id":
				f.Sequence = 2
				f.GetHeartbeat().ProcessState = agentdv1.Heartbeat_RUNNING
			}
			_ = s.Send(f)
			if _, err := s.Recv(); mode == "identical-replay" {
				if err != nil {
					t.Fatal("policy-approved identical replay rejected")
				}
				s.Close()
			} else if err == nil {
				t.Fatal("denied replay acknowledged")
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("replay stream did not close")
			}
			want := int32(1)
			if mode == "identical-replay" {
				want = 2
			}
			if received.Load() != want || policyCalls.Load() != 2 {
				t.Fatal("replay crossed policy gate")
			}
		})
	}
}

func FuzzEnvelopeCodec(f *testing.F) {
	for _, raw := range [][]byte{nil, {0x0f}, {0x3a, 0x7f, 1}, {0xf8, 0x07, 1}, {0x0b, 0x0c}, {0x08, 1}} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		m := new(agentdv1.Envelope)
		if (codec{}).Unmarshal(raw, m) != nil {
			return
		}
		if len(raw) > protocol.MaxFrameBytes || !protocol.KnownFields(m) {
			t.Fatal("codec bounds violated")
		}
		encoded, err := (codec{}).Marshal(m)
		if err != nil {
			t.Fatal("accepted frame cannot be encoded")
		}
		copy := new(agentdv1.Envelope)
		if (codec{}).Unmarshal(encoded, copy) != nil || !proto.Equal(m, copy) {
			t.Fatal("accepted frame changed on round trip")
		}
	})
}
