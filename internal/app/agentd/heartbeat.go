package agentd

import (
	"context"
	"errors"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

var ErrHeartbeat = errors.New("agentd heartbeat failed")

// RunHeartbeats sends an immediate snapshot, then coalesced periodic snapshots.
// Call only for an admitted connection. snapshot is bounded trusted code and
// returns a private value with current connection-local sequence/operation data.
// send must honor context and use the connection's shared outbound sequencer;
// it owns envelope construction, authoritative checks and stream closure.
// There is one send in flight, no retries, and no output-content dependency.
func RunHeartbeats(ctx context.Context, l *agentdv1.Limits, snapshot func() *agentdv1.Heartbeat, send func(context.Context, *agentdv1.Heartbeat) error) error {
	hard := protocol.HardLimits()
	if l == nil || l.HeartbeatIntervalMs == 0 || l.HeartbeatIntervalMs > hard.HeartbeatIntervalMs || l.LivenessWindowMs > hard.LivenessWindowMs || uint64(l.LivenessWindowMs) < 2*uint64(l.HeartbeatIntervalMs) || snapshot == nil || send == nil {
		return ErrHeartbeat
	}
	interval := time.Duration(l.HeartbeatIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var accepted, produced uint64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h := snapshot()
		if h == nil || !protocol.KnownFields(h) || h.ProcessState < agentdv1.Heartbeat_ABSENT || h.ProcessState > agentdv1.Heartbeat_FAILED || h.LastAcceptedSequence < accepted || h.LastProducedSequence < produced {
			return ErrHeartbeat
		}
		if h.ActiveOperationId != "" {
			if _, err := primitives.ParseID(h.ActiveOperationId); err != nil {
				return ErrHeartbeat
			}
		}
		accepted, produced = h.LastAcceptedSequence, h.LastProducedSequence
		h = proto.Clone(h).(*agentdv1.Heartbeat)
		attempt, cancel := context.WithTimeout(ctx, interval)
		result := make(chan error, 1)
		go func() { result <- send(attempt, h) }()
		select {
		case err := <-result:
			expired := attempt.Err()
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil || expired != nil {
				return ErrHeartbeat
			}
		case <-attempt.Done():
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrHeartbeat
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
