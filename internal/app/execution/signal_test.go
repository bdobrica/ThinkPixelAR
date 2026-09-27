package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type signalStateStore struct {
	persistence.Repositories
	e *domain.Execution
	s *session.Session
}

func (s *signalStateStore) WithinTransaction(ctx context.Context, _ primitives.ID, fn func(context.Context, persistence.Repositories) error) error {
	return fn(ctx, s)
}
func (s *signalStateStore) Executions() persistence.ExecutionRepository {
	return signalExecutions{e: s.e}
}
func (s *signalStateStore) Sessions() persistence.SessionRepository        { return signalSessions{s: s.s} }
func (s *signalStateStore) Idempotency() persistence.IdempotencyRepository { return signalKeys{} }

type signalExecutions struct {
	persistence.ExecutionRepository
	e *domain.Execution
}

func (r signalExecutions) Get(context.Context, primitives.ID) (*domain.Execution, error) {
	return r.e, nil
}
func (r signalExecutions) GetForUpdate(context.Context, primitives.ID) (*domain.Execution, error) {
	return r.e, nil
}

type signalSessions struct {
	persistence.SessionRepository
	s *session.Session
}

func (r signalSessions) GetForUpdate(context.Context, primitives.ID) (*session.Session, error) {
	return r.s, nil
}

type signalKeys struct {
	persistence.IdempotencyRepository
}

func (signalKeys) Reserve(_ context.Context, r *idempotency.Record) (*idempotency.Record, bool, error) {
	return r, true, nil
}

func TestSignalsRejectIllegalStateAndStaleGeneration(t *testing.T) {
	now := time.Now().UTC()
	tenant, _ := primitives.NewID(now)
	sid, _ := primitives.NewID(now)
	eid, _ := primitives.NewID(now)
	digest := sandbox.Digest(nil)
	runtime := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", AgentID: "test", AgentVersionID: "1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: digest, RuntimeSpecSchemaVersion: "1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: digest, RuntimeProfileSchemaVersion: "1"}
	sess, err := session.New(tenant, sid, runtime, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = sess.Transition(session.Ready, 0, now); err != nil {
		t.Fatal(err)
	}
	if err = sess.Transition(session.Active, 1, now); err != nil {
		t.Fatal(err)
	}
	store := &signalStateStore{s: sess}
	s, _ := NewLocalSignaler(store, clock.Fixed{Time: now}, func(context.Context, Caller, primitives.ID, primitives.ID) error { return nil }, func(context.Context, persistence.Repositories, Caller, *domain.Execution, SignalRequest) error {
		t.Fatal("illegal signal reached policy")
		return nil
	})
	caller := Caller{TenantID: tenant, PrincipalDigest: digest}
	request, _ := ParseSignal([]byte(`{"type":"interrupt","payload":{}}`))
	for _, state := range []domain.State{domain.Queued, domain.Materializing, domain.Cancelling, domain.TimingOut, domain.Succeeded, domain.Failed, domain.Cancelled, domain.TimedOut, domain.Running} {
		t.Run(string(state), func(t *testing.T) {
			b := domain.Binding{SessionID: sid, SessionGeneration: 1, AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", AuthorityReference: "grant", GrantDigest: digest, AgentID: "test", AgentVersionID: "1", AgentEvidence: []byte(`{}`), AgentEvidenceDigest: digest}
			if state == domain.Running {
				b.SessionGeneration = 2
			} // stale Session fence
			var terminal *time.Time
			var result *domain.TerminalResult
			switch state {
			case domain.Succeeded, domain.Failed, domain.Cancelled, domain.TimedOut:
				terminal = &now
				result = &domain.TerminalResult{Reference: "fixture", Digest: digest}
			}
			store.e, err = domain.Restore(tenant, eid, b, now.Add(time.Hour), state, 2, result, now, now, terminal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Signal(context.Background(), caller, eid, "signal-test-key-0001", request); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}
