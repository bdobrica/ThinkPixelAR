package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

func ConfigurationDigest(c Config) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type commandResult struct {
	digest string
	failed bool
	status ProcessStatus
}

// ProcessControl serializes the process capability and retains a bounded ledger
// for this supervisor lifetime. Supervisor loss requires Attempt replacement;
// the control-plane's durable ledger owns reconciliation across AR restarts.
type ProcessControl struct {
	gate          sync.Mutex
	operation     sync.Mutex
	processes     *Processes
	configuration string
	handle        string
	operations    map[string]commandResult
}

func NewProcessControl(c Config) (*ProcessControl, error) {
	if !slices.Contains(c.Capabilities, control.Capability) {
		return nil, ErrConfig
	}
	p, err := NewProcessesWithCapture(c, nil)
	if err != nil {
		return nil, err
	}
	return &ProcessControl{processes: p, configuration: ConfigurationDigest(c), operations: map[string]commandResult{}}, nil
}

// CheckClient rejects unsupported inbound messages before delivery. Sequence
// guards are installed inside Serve after Welcome fixes the connection identity.
func (p *ProcessControl) CheckClient(_ context.Context, f *agentdv1.Envelope) error {
	switch {
	case f.GetCommand() != nil:
		return control.Command(f, p.configuration)
	case f.GetRotation() != nil:
		if f.GetRotation().Kind != agentdv1.Rotation_ISSUED && f.GetRotation().Kind != agentdv1.Rotation_REQUEST {
			return control.ErrControl
		}
	case f.GetHeartbeat() != nil, f.GetAcknowledgement() != nil, f.GetObservation() != nil, f.GetFailure() != nil:
	default:
		return control.ErrControl
	}
	return nil
}
func (p *ProcessControl) execute(ctx context.Context, f *agentdv1.Envelope) commandResult {
	if !p.operation.TryLock() {
		return commandResult{failed: true}
	}
	defer p.operation.Unlock()
	if control.Command(f, p.configuration) != nil {
		return commandResult{failed: true}
	}
	if old, ok := p.operations[f.OperationId]; ok {
		if old.digest != f.RequestDigest {
			return commandResult{failed: true}
		}
		return old
	}
	if len(p.operations) >= 128 {
		return commandResult{failed: true}
	}
	result := commandResult{digest: f.RequestDigest, failed: true}
	// Reserve before invoking the operation: cancellation never authorizes replay.
	p.operations[f.OperationId] = result
	c := f.GetCommand()
	id := p.processes.Status().ProcessID
	err := control.ErrControl
	switch c.Kind {
	case agentdv1.Command_START:
		if p.handle != "" {
			break
		}
		p.handle = f.HarnessHandle
		_, err = p.processes.Start(ctx)
	case agentdv1.Command_STATUS:
		if p.handle != "" && p.handle != f.HarnessHandle {
			break
		}
		err = nil
	case agentdv1.Command_STOP, agentdv1.Command_INTERRUPT, agentdv1.Command_RESTART:
		if p.handle != f.HarnessHandle || id == "" {
			break
		}
		if c.Kind == agentdv1.Command_RESTART {
			_, err = p.processes.Restart(ctx, id)
		} else {
			err = p.processes.Stop(ctx, id)
		}
	default:
		return result
	}
	// A rejected handle must not look like a successful no-op.
	if p.handle != f.HarnessHandle && c.Kind != agentdv1.Command_STATUS {
		return result
	}
	result.failed = err != nil
	result.status = p.processes.Status()
	p.operations[f.OperationId] = result
	return result
}
func (p *ProcessControl) Disconnected(ctx context.Context) error {
	id := p.processes.Status().ProcessID
	if id == "" {
		return nil
	}
	err := p.processes.Stop(ctx, id)
	if err == nil || errors.Is(err, ErrProcessStale) {
		if capture, captureErr := p.processes.Output(id); captureErr == nil {
			capture.Close()
		}
		return nil
	}
	return err
}

// Serve has one sender and one receiver, one in-flight command, no offline queue.
// Heartbeats/renewal continue while process operations use their finite budgets.
func (p *ProcessControl) Serve(ctx context.Context, s *grpctransport.Session, b *TransportBootstrap, renew <-chan time.Time) error {
	if !p.gate.TryLock() {
		return ErrProcessBusy
	}
	defer p.gate.Unlock()
	if !slices.Contains(s.Welcome().Capabilities, control.Capability) {
		return ErrConfig
	}
	if !p.operation.TryLock() {
		return ErrProcessBusy
	}
	if !p.processes.gate.TryLock() {
		p.operation.Unlock()
		return ErrProcessBusy
	}
	p.processes.captureLimits = proto.Clone(s.Welcome().Limits).(*agentdv1.Limits)
	p.processes.gate.Unlock()
	p.operation.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.Close()
	frames := make(chan *agentdv1.Envelope)
	recvErr := make(chan error, 1)
	go func() {
		for {
			f, err := s.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	var input control.Sequences
	var output uint64
	var accepted uint64
	active := ""
	handle := ""
	send := func(f *agentdv1.Envelope) error {
		w := s.Welcome()
		mid, err := primitives.NewID(time.Now())
		if err != nil || output == ^uint64(0) {
			return control.ErrControl
		}
		output++
		f.Major = w.Major
		f.Minor = w.Minor
		f.Binding = proto.Clone(w.Binding).(*agentdv1.Binding)
		f.ConnectionId = w.ConnectionId
		f.ConnectionEpoch = w.ConnectionEpoch
		f.MessageId = string(mid)
		f.Sequence = output
		return s.Send(f)
	}
	heartbeat := func() error {
		h := p.processes.Heartbeat()
		h.LastAcceptedSequence = accepted
		h.LastProducedSequence = output
		h.ActiveOperationId = active
		return send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Heartbeat{Heartbeat: h}})
	}
	type captured struct {
		data Output
		err  error
	}
	captures := make(chan captured)
	var captureID primitives.ID
	var stopCapture context.CancelFunc
	var captureDone chan struct{}
	detachCapture := func() {
		if stopCapture != nil {
			stopCapture()
			<-captureDone
			stopCapture = nil
		}
	}
	defer detachCapture()
	attachCapture := func() error {
		id := p.processes.Status().ProcessID
		if id == "" || id == captureID {
			return nil
		}
		detachCapture()
		stream, err := p.processes.Output(id)
		if err != nil {
			return err
		}
		captureID = id
		var captureCtx context.Context
		captureCtx, stopCapture = context.WithCancel(ctx)
		captureDone = make(chan struct{})
		go func() {
			defer close(captureDone)
			for {
				out, err := stream.Receive(captureCtx)
				select {
				case captures <- captured{out, err}:
				case <-captureCtx.Done():
					clear(out.Data)
					return
				}
				if err != nil {
					return
				}
			}
		}()
		return nil
	}
	tick := time.NewTicker(time.Duration(s.Welcome().Limits.HeartbeatIntervalMs) * time.Millisecond)
	defer tick.Stop()
	type finished struct {
		frame  *agentdv1.Envelope
		result commandResult
	}
	results := make(chan finished, 1)
	busy := false
	renewalPending := false
	renewalDue := false
	rotate := func() error {
		if err := send(&agentdv1.Envelope{Body: &agentdv1.Envelope_Rotation{Rotation: &agentdv1.Rotation{Kind: agentdv1.Rotation_REQUEST}}}); err != nil {
			return err
		}
		renewalPending = true
		renewalDue = false
		return nil
	}
	defer func() {
		cancel()
		if busy {
			select {
			case <-results:
			case <-time.After(p.processes.stopBudget() + time.Duration(p.processes.config.StartTimeoutMS)*time.Millisecond):
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			return err
		case <-renew:
			renewalDue = true
			if !busy {
				if err := rotate(); err != nil {
					return err
				}
			}
		case out := <-captures:
			if out.err != nil {
				if errors.Is(out.err, io.EOF) {
					continue
				}
				return out.err
			}
			schema := control.Capability + "/stdout"
			kind := agentdv1.Observation_DIAGNOSTIC
			switch out.data.Source {
			case OutputStderr:
				schema = control.Capability + "/stderr"
			case OutputAdapterEvent:
				schema = control.Capability + "/event"
				kind = agentdv1.Observation_CANDIDATE_EVENT
			}
			err := send(&agentdv1.Envelope{HarnessHandle: handle, Body: &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: kind, PayloadSchema: schema, Payload: out.data.Data}}})
			clear(out.data.Data)
			if err != nil {
				return err
			}
		case <-tick.C:
			if err := heartbeat(); err != nil {
				return err
			}
		case f := <-frames:
			if input.Check(f) != nil {
				return control.ErrControl
			}
			accepted = f.Sequence
			if f.GetRotation() != nil {
				if !renewalPending || busy {
					return control.ErrControl
				}
				if err := b.InstallRotation(s, f); err != nil {
					return err
				}
				return nil
			}
			if f.GetAcknowledgement() != nil {
				continue
			}
			if f.GetCommand() == nil || busy || renewalPending || control.Command(f, p.configuration) != nil {
				return control.ErrControl
			}
			busy = true
			active = f.OperationId
			go func() {
				opctx, stop := context.WithDeadline(ctx, time.UnixMilli(f.DeadlineUnixMs))
				defer stop()
				results <- finished{f, p.execute(opctx, f)}
			}()
		case done := <-results:
			busy = false
			active = ""
			if !done.result.failed {
				handle = done.frame.HarnessHandle
				if err := attachCapture(); err != nil {
					return err
				}
			}
			reply := &agentdv1.Envelope{OperationId: done.frame.OperationId, RequestDigest: done.frame.RequestDigest, HarnessHandle: done.frame.HarnessHandle}
			if done.result.failed {
				reply.Body = &agentdv1.Envelope_Failure{Failure: &agentdv1.Failure{Code: agentdv1.Failure_OUTCOME_UNKNOWN}}
			} else {
				reply.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: done.frame.MessageId, RequestDigest: done.frame.RequestDigest, AcceptedSequence: done.frame.Sequence}}
			}
			if err := send(reply); err != nil {
				return err
			}
			if err := heartbeat(); err != nil {
				return err
			}
			if renewalDue {
				if err := rotate(); err != nil {
					return err
				}
			}
		}
	}
}
