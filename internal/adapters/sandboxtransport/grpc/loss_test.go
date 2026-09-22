package grpctransport

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
)

// lossConn drops encrypted traffic after a successful handshake, keeping TCP
// open. Successful local writes therefore cannot prove peer application health.
// It is private to the test dialer; no production fault-injection hook is added.
type lossConn struct {
	net.Conn
	dropRead, dropWrite atomic.Bool
}

func (c *lossConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if err != nil || !c.dropRead.Load() {
			return n, err
		}
	}
}
func (c *lossConn) Write(p []byte) (int, error) {
	if c.dropWrite.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func TestTransportLossCancelsAndReleasesLease(t *testing.T) {
	for _, mode := range []string{"socket-close", "server-stop", "drop-client-to-server", "drop-server-to-client", "drop-both"} {
		t.Run(mode, func(t *testing.T) {
			ca, clients := newIssuer(t), newIssuer(t)
			e := expectations()
			e.Limits.HeartbeatIntervalMs = 50
			e.Limits.LivenessWindowMs = 500
			var closes atomic.Int32
			released := make(chan struct{}, 2)
			handlerDone := make(chan struct{})
			address, stop := serve(t, ServerConfig{Certificate: ca.issue(t, true, nil), ServerName: testServer, TrustDomain: testDomain, ClientRoots: clients.roots, MaxConnections: 8, Authorizer: admitFunc(func(context.Context, transport.Peer, []byte) (transport.Lease, error) {
				l := lease(e)
				l.Close = func() { closes.Add(1); released <- struct{}{} }
				return l, nil
			}), Handle: func(_ context.Context, s *Session) error {
				defer close(handlerDone)
				for {
					f, err := s.Recv()
					if err != nil {
						return err
					}
					reply := frame(s.Welcome())
					reply.Sequence = f.Sequence
					reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId, AcceptedSequence: f.Sequence}}
					if err := s.Send(reply); err != nil {
						return err
					}
				}
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sockets := make(chan *lossConn, 8)
			dial := func(ctx context.Context, _ string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
				if err != nil {
					return nil, err
				}
				faulty := &lossConn{Conn: c}
				sockets <- faulty
				return faulty, nil
			}
			cfg := ClientConfig{Endpoint: "https://" + testServer, ServerName: testServer, TrustDomain: testDomain, ServerRoots: ca.roots, Certificate: clients.issue(t, false, nil), Expected: e, Hello: hello(e), Check: func(context.Context, *agentdv1.Envelope) error { return nil }}
			s, err := connect(ctx, cfg, dial)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			socket := <-sockets
			// Traffic must keep both watchdogs alive beyond their initial window.
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			f := frame(s.Welcome())
			for i := 0; i < 15; i++ {
				<-ticker.C
				f.Sequence++
				if err := s.Send(f); err != nil {
					t.Fatal("healthy send", err)
				}
				if _, err := s.Recv(); err != nil {
					t.Fatal("healthy receive", err)
				}
			}
			switch mode {
			case "socket-close":
				_ = socket.Close()
			case "server-stop":
				stop()
			case "drop-client-to-server":
				socket.dropWrite.Store(true)
			case "drop-server-to-client":
				socket.dropRead.Store(true)
			case "drop-both":
				socket.dropWrite.Store(true)
				socket.dropRead.Store(true)
			}
			// Continue local writes while a receive is pending. A silent direction must
			// still expire; the caller deadline is deliberately later than this bound.
			recvDone := make(chan error, 1)
			go func() { _, err := s.Recv(); recvDone <- err }()
			bound := time.NewTimer(2 * time.Second)
			defer bound.Stop()
		waiting:
			for {
				select {
				case <-ticker.C:
					f.Sequence++
					_ = s.Send(f)
				case err := <-recvDone:
					if err == nil {
						t.Fatal("unexpected reply after loss")
					}
					break waiting
				case <-bound.C:
					t.Fatal("receive outlived loss bound")
				}
			}
			if s.Context().Err() == nil {
				t.Fatal("session not canceled")
			}
			if ctx.Err() != nil {
				t.Fatal("caller deadline caused closure")
			}
			if s.Send(f) == nil {
				t.Fatal("closed session accepted send")
			}
			select {
			case <-released:
			case <-bound.C:
				t.Fatal("lease not released")
			}
			select {
			case <-handlerDone:
			case <-bound.C:
				t.Fatal("handler did not exit")
			}
			s.Close()
			if closes.Load() != 1 {
				t.Fatal("lease release count", closes.Load())
			}
		})
	}
}
