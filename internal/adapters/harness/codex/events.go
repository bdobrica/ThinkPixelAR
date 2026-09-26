package codex

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// EventPolicy is trusted composition, never vendor configuration. It must handle
// secrets split across fragments and may return empty to suppress content. Errors
// fail the stream without exposing policy diagnostics. Reference stores only
// policy-approved content in protected storage; it must not return vendor paths.
// Nil policy suppresses all message text and process output. Tool arguments,
// results, commands, prompts and reasoning are never passed to this interface.
type EventPolicy interface {
	MessageText(context.Context, primitives.ID, string) (string, error)
	ProcessOutput(context.Context, primitives.ID, string) (string, error)
}

// ResultReferencePolicy optionally extends EventPolicy. Trusted composition may
// finalize an artifact from content it already approved for this operation. It
// must enforce protected storage/access and context bounds. No vendor turn,
// error, path or reasoning is supplied. Empty means no captured reference.
type ResultReferencePolicy interface {
	ResultReference(context.Context, primitives.ID, string) (string, error)
}

// Events binds the accepted turn to trusted correlation. The caller must verify
// the current authority/fence before opening and again before publishing each
// candidate. This pull stream does not allocate durable Session sequence numbers.
// There is one subscription per process/turn; reconnect/replay is unsupported.
func (c *Client) Events(handle harness.HarnessHandle, operation runtimebinding.OperationIdentity, capabilities harness.Capabilities, limits harness.AdapterLimits, policy EventPolicy) (harness.HarnessEventStream, error) {
	if !c.gate.TryLock() {
		return nil, harness.ErrConflict
	}
	defer c.gate.Unlock()
	if c.eventsOpened {
		return nil, harness.ErrConflict
	}
	if c.turnID == "" || string(operation.ID) != c.turnOperation || !eventBinding(handle, operation) || handle.VendorSessionReference != c.threadID {
		return nil, harness.ErrInvalid
	}
	if capabilities.Require(harness.StructuredEvents, harness.Streaming) != nil {
		return nil, harness.ErrUnsupported
	}
	if limits.EventBytes < 256 || limits.EventBytes > runtimeevent.MaxPayloadBytes || limits.EventsPerSecond < 1 || limits.EventsPerSecond > 1000 || limits.BufferedEvents < 1 {
		return nil, harness.ErrInvalid
	}
	id, err := primitives.NewID(time.Now())
	if err != nil {
		return nil, harness.ErrOutcomeUnknown
	}
	caps := harness.Capabilities{}
	for k, v := range capabilities {
		caps[k] = v
	}
	c.eventsOpened = true
	return &eventStream{client: c, handle: handle, operation: operation, capabilities: caps, limits: limits, policy: policy, id: id, items: map[string]*eventItem{}, closed: make(chan struct{})}, nil
}

func eventBinding(h harness.HarnessHandle, op runtimebinding.OperationIdentity) bool {
	for _, id := range []primitives.ID{h.ID, h.ProcessInstanceID, h.Fence.TenantID, h.Fence.SessionID, h.Fence.ExecutionID, h.Fence.AttemptID, h.Fence.SandboxBindingID, op.ID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false
		}
	}
	return h.AdapterKind == Kind && h.Fence.Generation > 0 && h.Fence.AttemptOrdinal > 0 && eventDigest(op.RequestDigest) && eventDigest(h.AdapterBuildDigest) && eventDigest(h.NegotiationDigest)
}

func eventDigest(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") {
		return false
	}
	for _, r := range s[7:] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

type eventStream struct {
	client       *Client
	handle       harness.HarnessHandle
	operation    runtimebinding.OperationIdentity
	capabilities harness.Capabilities
	limits       harness.AdapterLimits
	policy       EventPolicy
	id           primitives.ID
	gate         sync.Mutex
	once         sync.Once
	closed       chan struct{}
	reading      atomic.Bool
	sequence     uint64
	started, end bool
	failed       error
	items        map[string]*eventItem
	nextRead     time.Time
	usage        *harness.UsagePayload
	completion   *harness.ObservationPayload
}

func (*eventStream) String() string     { return "[restricted Codex events]" }
func (s *eventStream) GoString() string { return s.String() }

// Close stops the subscription. Closing a pending read invalidates the protocol
// connection, but never signals or waits for the child. Its supervisor still owns
// termination; a closed/failed stream cannot be reattached to this disposable turn.
func (s *eventStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
		if s.reading.Load() {
			s.client.Close()
		}
	})
	return nil
}

func (s *eventStream) Next(ctx context.Context) (event harness.HarnessEvent, err error) {
	if !s.gate.TryLock() {
		return event, harness.ErrConflict
	}
	defer s.gate.Unlock()
	if s.failed != nil {
		return event, s.failed
	}
	select {
	case <-s.closed:
		return event, io.EOF
	default:
	}
	if s.end {
		return event, io.EOF
	}
	if !s.client.gate.TryLock() {
		return event, harness.ErrConflict
	}
	defer s.client.gate.Unlock()
	// A stalled subscriber does not read ahead. A stalled vendor has a finite
	// per-Next budget; caller deadlines can narrow it.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, s.client.Close)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = harness.ErrOutcomeUnknown
		}
		if err != nil && err != io.EOF {
			s.failed = err
			s.client.Close()
		}
	}()
	if s.completion != nil {
		return s.finish(*s.completion)
	}
	for {
		if wait := time.Until(s.nextRead); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return event, harness.ErrOutcomeUnknown
			case <-s.closed:
				timer.Stop()
				return event, io.EOF
			}
		}
		s.nextRead = time.Now().Add(time.Second / time.Duration(s.limits.EventsPerSecond))
		s.reading.Store(true)
		select {
		case <-s.closed:
			s.reading.Store(false)
			return event, io.EOF
		case <-ctx.Done():
			s.reading.Store(false)
			return event, harness.ErrOutcomeUnknown
		default:
		}
		raw, readErr := s.client.readEventFrame()
		s.reading.Store(false)
		if readErr != nil {
			clear(raw)
			return event, harness.ErrStreamIntegrity // EOF without turn/completed is a gap.
		}
		event, err = s.normalize(ctx, raw)
		clear(raw)
		if err != nil || s.end {
			if err == nil && event.Type == "" {
				err = io.EOF
			}
			return event, err
		}
		if event.Type != "" {
			return event, nil
		}
	}
}

func (s *eventStream) emit(kind string, payload any) (harness.HarnessEvent, error) {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > s.limits.EventBytes || s.sequence == ^uint64(0) {
		return harness.HarnessEvent{}, harness.ErrStreamIntegrity
	}
	now := time.Now().UTC()
	id, err := primitives.NewID(now)
	if err != nil {
		return harness.HarnessEvent{}, harness.ErrOutcomeUnknown
	}
	e := harness.HarnessEvent{StreamID: s.id, EventID: id, Operation: s.operation, Handle: s.handle, Sequence: s.sequence + 1, Type: kind, SchemaVersion: harness.EventSchemaVersion, OccurredAt: now, ObservedAt: now, Content: harness.Content{Schema: harness.EventSchemaVersion, Classification: runtimeevent.Confidential, Inline: raw}}
	if _, err := harness.CheckEventDeclaration(e, s.capabilities); err != nil {
		return harness.HarnessEvent{}, err
	}
	s.sequence++
	return e, nil
}

func eventText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' })
}
