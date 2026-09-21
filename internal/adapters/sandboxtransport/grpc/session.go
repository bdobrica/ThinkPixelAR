package grpctransport

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"
)

type wire interface {
	Send(*agentdv1.Envelope) error
	Recv() (*agentdv1.Envelope, error)
}

// Session owns one stream, not authoritative lifecycle state. One send and one
// receive may run concurrently. It never queues frames or logs payloads.
type Session struct {
	ctx                context.Context
	cancel             context.CancelFunc
	stream             wire
	welcome            *agentdv1.Welcome
	server             bool
	check              func(context.Context, *agentdv1.Envelope) error
	sendMu, recvMu     sync.Mutex
	sendRate, recvRate *rate.Limiter
	close              func()
	activity           chan struct{}
	once               sync.Once
}

func (s *Session) Context() context.Context   { return s.ctx }
func (s *Session) Welcome() *agentdv1.Welcome { return proto.Clone(s.welcome).(*agentdv1.Welcome) }
func (s *Session) Close() {
	s.once.Do(func() {
		s.cancel()
		if s.close != nil {
			s.close()
		}
	})
}
func (s *Session) Send(frame *agentdv1.Envelope) error {
	if !s.sendMu.TryLock() {
		return ErrTransport
	}
	defer s.sendMu.Unlock()
	if s.sendRate == nil || !s.sendRate.Allow() || !s.valid(frame, s.server) {
		s.Close()
		return ErrTransport
	}
	copy := proto.Clone(frame).(*agentdv1.Envelope)
	if s.authorize(copy) != nil {
		s.Close()
		return ErrTransport
	}
	_, err := bounded(s.ctx, s.window(), func() (struct{}, error) { return struct{}{}, s.stream.Send(copy) })
	if err != nil {
		s.Close()
		return ErrTransport
	}
	return nil
}
func (s *Session) Recv() (*agentdv1.Envelope, error) {
	if !s.recvMu.TryLock() {
		return nil, ErrTransport
	}
	defer s.recvMu.Unlock()
	frame, err := bounded(s.ctx, s.window(), s.stream.Recv)
	if err != nil || s.recvRate == nil || !s.recvRate.Allow() || !s.valid(frame, !s.server) {
		s.Close()
		return nil, ErrTransport
	}
	if s.authorize(frame) != nil {
		s.Close()
		return nil, ErrTransport
	}
	if s.activity != nil {
		select {
		case s.activity <- struct{}{}:
		default:
		}
	}
	return proto.Clone(frame).(*agentdv1.Envelope), nil
}
func (s *Session) authorize(frame *agentdv1.Envelope) error {
	if s.ctx.Err() != nil {
		return ErrTransport
	}
	_, err := bounded(s.ctx, s.window(), func() (struct{}, error) { return struct{}{}, s.check(s.ctx, proto.Clone(frame).(*agentdv1.Envelope)) })
	return err
}
func (s *Session) window() time.Duration {
	return time.Duration(s.welcome.Limits.LivenessWindowMs) * time.Millisecond
}
func (s *Session) valid(f *agentdv1.Envelope, fromServer bool) bool {
	if f == nil || !protocol.KnownFields(f) || proto.Size(f) > int(s.welcome.Limits.FrameBytes) || f.Major != s.welcome.Major || f.Minor != s.welcome.Minor || f.ConnectionId != s.welcome.ConnectionId || f.ConnectionEpoch != s.welcome.ConnectionEpoch || f.Sequence == 0 || !proto.Equal(f.Binding, s.welcome.Binding) {
		return false
	}
	if _, err := primitives.ParseID(f.MessageId); err != nil {
		return false
	}
	switch body := f.Body.(type) {
	case *agentdv1.Envelope_Command:
		c := body.Command
		if c == nil {
			return false
		}
		if c.Kind != agentdv1.Command_STATUS {
			if _, err := primitives.ParseID(f.OperationId); err != nil {
				return false
			}
			if !requestDigest(f.RequestDigest) || f.SentUnixMs <= 0 || f.DeadlineUnixMs <= f.SentUnixMs || f.DeadlineUnixMs <= time.Now().UnixMilli() {
				return false
			}
		}
		return fromServer && c.Kind >= agentdv1.Command_START && c.Kind <= agentdv1.Command_SIGNAL && len(c.Payload) <= int(s.welcome.Limits.CommandBytes) && !(len(c.Payload) > 0 && c.ArtifactReference != "")
	case *agentdv1.Envelope_Observation:
		o := body.Observation
		if fromServer || o == nil || o.Kind < agentdv1.Observation_PROCESS_STATUS || o.Kind > agentdv1.Observation_DIAGNOSTIC {
			return false
		}
		max := s.welcome.Limits.EventBytes
		if o.Kind == agentdv1.Observation_DIAGNOSTIC {
			max = s.welcome.Limits.DiagnosticBytes
		}
		return len(o.Payload) <= int(max) && !(len(o.Payload) > 0 && o.ArtifactReference != "")
	case *agentdv1.Envelope_Heartbeat:
		return !fromServer && body.Heartbeat != nil && body.Heartbeat.ProcessState >= agentdv1.Heartbeat_ABSENT && body.Heartbeat.ProcessState <= agentdv1.Heartbeat_FAILED
	case *agentdv1.Envelope_Acknowledgement:
		return body.Acknowledgement != nil && body.Acknowledgement.EventCredit <= s.welcome.Limits.BufferedEvents
	case *agentdv1.Envelope_Failure:
		return body.Failure != nil && body.Failure.Code >= agentdv1.Failure_INCOMPATIBLE && body.Failure.Code <= agentdv1.Failure_OUTCOME_UNKNOWN
	// Rotation is reserved until the issuance/renewal implementation can validate it.
	default:
		return false
	}
}
func bounded[T any](ctx context.Context, limit time.Duration, call func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, ErrTransport
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() { v, err := call(); done <- result{v, err} }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return zero, ErrTransport
	case <-timer.C:
		return zero, ErrTransport
	case r := <-done:
		return r.value, r.err
	}
}

func requestDigest(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s[7:])
	return err == nil
}

// watch requires peer application traffic, not merely local writes or TCP ACKs.
// Heartbeat production is a later supervisor responsibility; quiet peers close.
func (s *Session) watch() {
	s.activity = make(chan struct{}, 1)
	go func() {
		defer s.Close()
		timer := time.NewTimer(s.window())
		defer timer.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-timer.C:
				return
			case <-s.activity:
				timer.Reset(s.window())
			}
		}
	}()
}
