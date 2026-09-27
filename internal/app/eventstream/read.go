// Package eventstream reads durable observations without granting execution authority.
package eventstream

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"

	"github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrInvalid     = errors.New("invalid event cursor")
	ErrNotFound    = errors.New("stream not found")
	ErrUnavailable = errors.New("stream unavailable")
	ErrGap         = errors.New("event replay gap")
)

// Access must check current Session disclosure rights on every call. With an
// event, it must also check classification/type-specific payload disclosure and
// external-sink validation/redaction policy. Deny rather than silently skip.
// A nil event checks Session access only. This trusted dependency is mandatory.
type Access func(context.Context, session.Caller, primitives.ID, *runtimeevent.Event) error

var principalDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Reader struct {
	store  persistence.TransactionManager
	clock  clock.Clock
	access Access
}
type Result struct {
	Earliest, Latest uint64
	// Sequence advances the internal scan even when Data is empty (filtered event).
	Sequence uint64
	Type     runtimeevent.Type
	Data     []byte
}

func NewReader(store persistence.TransactionManager, c clock.Clock, access Access) (*Reader, error) {
	if store == nil || c == nil || access == nil {
		return nil, ErrUnavailable
	}
	return &Reader{store, c, access}, nil
}

// ExecutionReader filters the parent Session log without renumbering events.
// Both Session/event disclosure and Execution disclosure dependencies are required.
type ExecutionReader struct {
	reader *Reader
	access execution.ReadAccess
}

func NewExecutionReader(store persistence.TransactionManager, c clock.Clock, access Access, executionAccess execution.ReadAccess) (*ExecutionReader, error) {
	r, err := NewReader(store, c, access)
	if err != nil || executionAccess == nil {
		return nil, ErrUnavailable
	}
	return &ExecutionReader{r, executionAccess}, nil
}

func (r *ExecutionReader) Next(ctx context.Context, caller session.Caller, id primitives.ID, after uint64) (Result, error) {
	return r.reader.next(ctx, caller, id, after, r.access)
}

func (r *Reader) Next(ctx context.Context, caller session.Caller, id primitives.ID, after uint64) (Result, error) {
	return r.next(ctx, caller, id, after, nil)
}

func (r *Reader) next(ctx context.Context, caller session.Caller, id primitives.ID, after uint64, executionAccess execution.ReadAccess) (Result, error) {
	var out Result
	if _, err := primitives.ParseID(string(caller.TenantID)); err != nil {
		return out, ErrNotFound
	}
	if _, err := primitives.ParseID(string(id)); err != nil || after > math.MaxInt64 {
		return out, ErrInvalid
	}
	if !principalDigest.MatchString(caller.PrincipalDigest) {
		return out, ErrNotFound
	}
	err := r.store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		id := id // Keep resource resolution local to this transaction callback.
		var executionID primitives.ID
		if executionAccess != nil {
			e, err := repos.Executions().Get(ctx, id)
			if err != nil {
				return err
			}
			if e == nil || e.ID() != id || e.TenantID() != caller.TenantID {
				return ErrNotFound
			}
			executionID, id = id, e.Binding().SessionID
			if executionAccess(ctx, caller, id, executionID) != nil {
				return ErrNotFound
			}
		}
		s, err := repos.Sessions().Get(ctx, id)
		if err != nil {
			return err
		}
		if s == nil || s.ID() != id || s.TenantID() != caller.TenantID || r.access(ctx, caller, id, nil) != nil {
			return ErrNotFound
		}
		read, err := repos.RuntimeEvents().ReadNext(ctx, id, after, r.clock.Now())
		if err != nil {
			return err
		}
		out.Earliest, out.Latest = read.Earliest, read.Latest
		if after > read.Latest {
			return ErrInvalid
		}
		if after+1 < read.Earliest || (read.Event == nil && after < read.Latest) {
			return ErrGap
		}
		e := read.Event
		if e == nil {
			return nil
		}
		if e.TenantID() != caller.TenantID || e.SessionID() != id {
			return ErrUnavailable
		}
		if e.Sequence() != after+1 {
			return ErrGap
		}
		if until, ok := e.RetainUntil(); ok && !until.After(r.clock.Now()) {
			return ErrGap
		}
		if executionID != "" && e.ExecutionID() != executionID {
			out.Sequence = e.Sequence()
			return nil
		}
		if r.access(ctx, caller, id, e) != nil {
			return ErrNotFound
		}
		if until, ok := e.RetainUntil(); ok && !until.After(r.clock.Now()) {
			return ErrGap
		}
		out.Sequence, out.Type = e.Sequence(), e.Type()
		c := e.Correlation()
		correlation := map[string]string{}
		if c.RequestID != "" {
			correlation["request_id"] = string(c.RequestID)
		}
		if c.TraceID != "" {
			correlation["trace_id"] = c.TraceID
		}
		if c.SpanID != "" {
			correlation["span_id"] = c.SpanID
		}
		envelope := map[string]any{"schema_version": runtimeevent.SchemaVersion, "event_id": e.EventID(), "tenant_id": e.TenantID(), "session_id": e.SessionID(), "sequence": e.Sequence(), "aggregate_version": e.AggregateVersion(), "type": e.Type(), "occurred_at": e.OccurredAt(), "recorded_at": e.RecordedAt(), "source": e.Source(), "classification": e.Classification(), "payload": json.RawMessage(e.Payload()), "correlation": correlation}
		if e.ExecutionID() != "" {
			envelope["execution_id"] = e.ExecutionID()
		}
		if e.AttemptID() != "" {
			envelope["attempt_id"] = e.AttemptID()
		}
		out.Data, err = json.Marshal(envelope)
		if len(out.Data) > 512*1024 {
			return ErrUnavailable
		}
		return err
	})
	if err != nil {
		out.Data = nil
		switch {
		case errors.Is(err, ErrGap):
			return out, ErrGap
		case errors.Is(err, ErrInvalid):
			return Result{}, ErrInvalid
		case errors.Is(err, ErrNotFound), errors.Is(err, persistence.ErrNotFound):
			return Result{}, ErrNotFound
		default:
			return Result{}, ErrUnavailable
		}
	}
	return out, nil
}
