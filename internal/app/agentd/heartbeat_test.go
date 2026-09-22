package agentd

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

func TestHeartbeatsPeriodicPrivateAndCancelable(t *testing.T) {
	l := protocol.HardLimits()
	l.HeartbeatIntervalMs = 10
	l.LivenessWindowMs = 40
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	original := &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_RUNNING, ActiveOperationId: "01950000-0000-7000-8000-000000000001"}
	sent := 0
	began := time.Now()
	err := RunHeartbeats(ctx, l, func() *agentdv1.Heartbeat {
		original.LastAcceptedSequence = uint64(sent)
		original.LastProducedSequence = uint64(sent * 2)
		return original
	}, func(ctx context.Context, h *agentdv1.Heartbeat) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded send")
		}
		if h.ProcessState != agentdv1.Heartbeat_RUNNING || h.LastAcceptedSequence != uint64(sent) || h.LastProducedSequence != uint64(sent*2) {
			t.Error("wrong progress")
		}
		h.ProcessState = agentdv1.Heartbeat_FAILED
		sent++
		if sent == 3 {
			cancel()
		}
		return nil
	})
	if err != context.Canceled || sent != 3 || time.Since(began) < 15*time.Millisecond {
		t.Fatal("periodic/cancellation failed", err, sent)
	}
	if original.ProcessState != agentdv1.Heartbeat_RUNNING {
		t.Fatal("sender mutated snapshot")
	}
}
func TestHeartbeatSendFailureIsBounded(t *testing.T) {
	l := protocol.HardLimits()
	l.HeartbeatIntervalMs = 20
	l.LivenessWindowMs = 50
	for _, which := range []string{"error", "timeout"} {
		t.Run(which, func(t *testing.T) {
			var calls atomic.Int32
			release := make(chan struct{})
			defer close(release)
			began := time.Now()
			err := RunHeartbeats(context.Background(), l, func() *agentdv1.Heartbeat { return &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_ABSENT} }, func(context.Context, *agentdv1.Heartbeat) error {
				calls.Add(1)
				if which == "timeout" {
					<-release
				}
				return errors.New("restricted-canary")
			})
			if err != ErrHeartbeat || strings.Contains(err.Error(), "canary") || calls.Load() != 1 || time.Since(began) > time.Second {
				t.Fatal("unbounded/unsafe send", err)
			}
		})
	}
}
func TestHeartbeatsRejectInvalidSnapshotsAndLimits(t *testing.T) {
	for _, which := range []string{"state", "operation", "accepted-regression", "produced-regression", "unknown", "nil", "limits"} {
		t.Run(which, func(t *testing.T) {
			l := protocol.HardLimits()
			l.HeartbeatIntervalMs = 1
			l.LivenessWindowMs = 5
			if which == "limits" {
				l.LivenessWindowMs = 1
			}
			calls := 0
			err := RunHeartbeats(context.Background(), l, func() *agentdv1.Heartbeat {
				h := &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_ABSENT, LastAcceptedSequence: 2, LastProducedSequence: 2}
				switch which {
				case "nil":
					return nil
				case "state":
					h.ProcessState = 99
				case "operation":
					h.ActiveOperationId = "restricted-canary"
				case "unknown":
					h.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
				case "accepted-regression":
					if calls > 0 {
						h.LastAcceptedSequence = 1
					}
				case "produced-regression":
					if calls > 0 {
						h.LastProducedSequence = 1
					}
				}
				return h
			}, func(context.Context, *agentdv1.Heartbeat) error { calls++; return nil })
			if err != ErrHeartbeat {
				t.Fatal("invalid heartbeat accepted")
			}
			expected := 0
			if strings.Contains(which, "regression") {
				expected = 1
			}
			if calls != expected {
				t.Fatal("invalid heartbeat sent")
			}
		})
	}
}
