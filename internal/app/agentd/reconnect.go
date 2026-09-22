package agentd

import (
	"bytes"
	"context"
	"crypto/x509"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
)

// ConnectionHooks are trusted composition. Serve owns frame sequencing and must
// send a Rotation REQUEST when renewal fires, install only a received ISSUED
// reply, and return to reconnect with the new key. All hooks honor context and
// never retain/log credentials. Disconnected stops/fences local work before any
// retry. Recovery only loads AR-projected material; it cannot issue authority.
type ConnectionHooks struct {
	Check        func(context.Context, *agentdv1.Envelope) error
	Serve        func(context.Context, *grpctransport.Session, *TransportBootstrap, <-chan time.Time) error
	Disconnected func(context.Context) error
	Recovery     func(context.Context) (*TransportBootstrap, error)
}

// RunConnections requires a finite authority/freshness deadline. It keeps keys
// only in memory, bounds each reconnect burst to eight attempts and uses jittered
// exponential backoff capped at five seconds. There is no offline command queue.
// A failed initial handshake is ambiguous: discard its proof and attempt only
// credential reconnect, never replay bootstrap consumption.
func RunConnections(ctx context.Context, b *TransportBootstrap, h ConnectionHooks) error {
	return runConnections(ctx, b, h, grpctransport.Connect)
}
func runConnections(ctx context.Context, b *TransportBootstrap, h ConnectionHooks, connect func(context.Context, grpctransport.ClientConfig) (*grpctransport.Session, error)) error {
	if b == nil || len(b.certificate.Certificate) == 0 || h.Check == nil || h.Serve == nil || h.Disconnected == nil {
		return ErrConfig
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrConfig
	}
	defer b.Destroy()
	recovered := false
	failures := 0
	for ctx.Err() == nil {
		leaf, err := x509.ParseCertificate(b.certificate.Certificate[0])
		if err != nil {
			return ErrConfig
		}
		if time.Until(leaf.NotAfter) <= 5*time.Second {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			stopErr := h.Disconnected(cleanup)
			cancel()
			if stopErr != nil {
				return ErrProcess
			}

			if recovered || h.Recovery == nil {
				return ErrConfig
			}
			if wait := time.Until(leaf.NotAfter); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
			}
			recovered = true
			next, err := h.Recovery(ctx)
			if err != nil || next == nil || next == b {
				return ErrConfig
			}
			if !sameStartupConfig(b.Config(), next.Config()) || len(next.proof) != 32 {
				next.Destroy()
				return ErrConfig
			}
			if _, err := next.ClientConfig(h.Check); err != nil {
				next.Destroy()
				return ErrConfig
			}
			b.Destroy()
			*b = *next
			*next = TransportBootstrap{}
		}
		config, err := b.ClientConfig(h.Check)
		if err != nil {
			return ErrConfig
		}
		// Keep only transport alive for bounded stop/reporting on a local signal.
		// The original authority deadline remains an absolute upper bound.
		deadline, _ := ctx.Deadline()
		wireCtx, closeWire := context.WithDeadline(context.WithoutCancel(ctx), deadline)
		var established atomic.Bool
		stopWatch := context.AfterFunc(ctx, func() {
			if !established.Load() {
				closeWire()
				return
			}
			select {
			case <-wireCtx.Done():
			case <-time.After(5 * time.Second):
				closeWire()
			}
		})
		s, err := connect(wireCtx, config)
		clear(config.Hello.BootstrapProof)
		clear(b.proof)
		b.proof = nil
		if err == nil {
			if !slices.Contains(s.Welcome().Capabilities, "rotation.v1") || b.Connected(s) != nil {
				s.Close()
				stopWatch()
				closeWire()
				return ErrConfig
			}
			established.Store(true)
			started := time.Now()
			previous := append([]byte(nil), b.certificate.Certificate[0]...)
			timer := time.NewTimer(max(time.Until(b.RotationAt()), 0))
			expiry, _ := s.Context().Deadline()
			serveCtx, stop := context.WithDeadline(s.Context(), expiry.Add(-5*time.Second))
			unwatch := context.AfterFunc(ctx, stop)
			_ = h.Serve(serveCtx, s, b, timer.C)
			unwatch()
			stop()
			timer.Stop()
			s.Close()
			if time.Since(started) >= time.Second || !bytes.Equal(previous, b.certificate.Certificate[0]) {
				failures = 0
			}
		}
		stopWatch()
		closeWire()
		// Even a failed/ambiguous handshake cannot leave autonomous work running.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = h.Disconnected(cleanup)
		cancel()
		if err != nil {
			return ErrProcess
		}
		if ctx.Err() != nil {
			return nil
		}
		failures++
		if failures >= 8 {
			return grpctransport.ErrTransport
		}
		delay := min(100*time.Millisecond*time.Duration(1<<uint(failures-1)), 5*time.Second)
		delay = delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
