// Package agentdserver hosts trusted AR-side transport application handling.
package agentdserver

import (
	"context"
	"errors"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrSession = errors.New("agentd session stopped")

type Rotation interface {
	Rotate(context.Context, transport.Peer, primitives.ID, uint64, *agentdv1.Rotation) (*agentdv1.Rotation, error)
}

// Command is trusted control-plane intent. Supplying it never bypasses the
// Session's current-authority checks and durable claim before Send.
type Command struct {
	OperationID, ConfigurationDigest, HarnessHandle string
	Kind                                            agentdv1.Command_Kind
	Deadline                                        time.Time
}

type Handler struct {
	Rotation Rotation
	Outcomes transport.CommandOutcomes
	// Plan returns a copied, bounded operator plan for an already admitted binding.
	// It is not a grant issuer or a sandbox-supplied command queue.
	Plan func(context.Context, transport.Peer) ([]Command, error)
	// Observe consumes untrusted hints synchronously with bounded work. It must
	// never promote process reports to authoritative Attempt/Execution state.
	Observe func(context.Context, *agentdv1.Envelope) error
}

func (h Handler) Serve(ctx context.Context, s *grpctransport.Session) error {
	if h.Rotation == nil || h.Outcomes == nil || h.Plan == nil || h.Observe == nil || s == nil {
		return ErrSession
	}
	defer s.Close()
	plan, err := h.Plan(ctx, s.Peer())
	if err != nil || len(plan) > 128 {
		return ErrSession
	}
	w := s.Welcome()
	deadline, ok := s.Context().Deadline()
	if !ok {
		return ErrSession
	}
	var sequence uint64
	send := func(f *agentdv1.Envelope) error {
		id, err := primitives.NewID(time.Now())
		if err != nil {
			return ErrSession
		}
		sequence++
		f.Major, f.Minor = w.Major, w.Minor
		f.Binding, f.ConnectionId, f.ConnectionEpoch = w.Binding, w.ConnectionId, w.ConnectionEpoch
		f.MessageId, f.Sequence, f.SentUnixMs = string(id), sequence, time.Now().UnixMilli()
		if f.DeadlineUnixMs == 0 || f.DeadlineUnixMs > deadline.UnixMilli() {
			f.DeadlineUnixMs = deadline.UnixMilli()
		}
		return s.Send(f)
	}
	type received struct {
		frame *agentdv1.Envelope
		err   error
	}
	input := make(chan received, 1)
	go func() {
		for {
			f, e := s.Recv()
			select {
			case input <- received{f, e}:
			case <-s.Context().Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	// Bound dispatch polling and serialize commands with incoming renewal.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	index := 0
	pending := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.Context().Done():
			return nil
		case <-ticker.C:
			if pending != "" || index == len(plan) {
				continue
			}
			c := plan[index]
			digest := control.Digest(c.Kind, c.ConfigurationDigest, c.HarnessHandle)
			outcome, readErr := h.Outcomes.CommandOutcome(ctx, s.Peer().Identity.TenantID, s.Peer().Identity.SandboxID, primitives.ID(c.OperationID), digest)
			if readErr == nil {
				if outcome != transport.DispatchAcknowledged {
					return ErrSession
				}
				index++
				continue
			}
			if !errors.Is(readErr, transport.ErrCommandNotFound) {
				return ErrSession
			}
			// An absent outcome is never itself permission. Send's
			// concrete frame policy must atomically claim the new operation; any
			// existing, conflicting or ambiguous claim rejects before delivery.
			f := &agentdv1.Envelope{OperationId: c.OperationID, HarnessHandle: c.HarnessHandle, RequestDigest: digest, DeadlineUnixMs: c.Deadline.UnixMilli(), Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: c.Kind, ConfigurationDigest: c.ConfigurationDigest, PayloadSchema: control.Capability}}}
			f.SentUnixMs = time.Now().UnixMilli()
			if control.Command(f, c.ConfigurationDigest) != nil || send(f) != nil {
				return ErrSession
			}
			pending = c.OperationID
		case result := <-input:
			if result.err != nil {
				return ErrSession
			}
			f := result.frame
			if r := f.GetRotation(); r != nil {
				if pending != "" {
					return ErrSession
				}
				reply, err := h.Rotation.Rotate(ctx, s.Peer(), primitives.ID(w.ConnectionId), w.ConnectionEpoch, r)
				if err != nil {
					return ErrSession
				}
				err = send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Rotation{Rotation: reply}})
				clear(reply.PrivateKeyPem)
				clear(reply.CertificatePem)
				if err != nil {
					return err
				}
				// Keep the stream alive while agentd installs the reply. Closing
				// immediately can race its credential installation with EOF.
				settle := time.NewTimer(5 * time.Second)
				defer settle.Stop()
				select {
				case <-ctx.Done():
				case <-s.Context().Done():
				case <-settle.C:
				}
				return nil
			}
			if f.OperationId != "" && f.OperationId == pending {
				if f.GetFailure() != nil {
					return ErrSession
				}
				if f.GetAcknowledgement() != nil {
					pending = ""
					index++
				}
			}
			if f.GetHeartbeat() != nil || f.GetObservation() != nil {
				if h.Observe(ctx, f) != nil {
					return ErrSession
				}
				if send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId, AcceptedSequence: f.Sequence}}}) != nil {
					return ErrSession
				}
			}
		}
	}
}
