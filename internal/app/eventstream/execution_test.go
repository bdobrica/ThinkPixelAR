package eventstream

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type executionRepo struct {
	persistence.ExecutionRepository
	e *execution.Execution
}

func (r executionRepo) Get(context.Context, primitives.ID) (*execution.Execution, error) {
	return r.e, nil
}

type executionFixture struct {
	*fixture
	e *execution.Execution
}

func (f *executionFixture) Executions() persistence.ExecutionRepository { return executionRepo{e: f.e} }
func (f *executionFixture) WithinTransaction(ctx context.Context, _ primitives.ID, fn func(context.Context, persistence.Repositories) error) error {
	return fn(ctx, f)
}

func TestExecutionFilterAndAccess(t *testing.T) {
	now := time.Now().UTC()
	id := func() primitives.ID { v, _ := primitives.NewID(now); return v }
	tenant, sid, eid, other := id(), id(), id(), id()
	digest := sandbox.Digest(nil)
	s, err := session.New(tenant, sid, session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "local", AgentID: "test", AgentVersionID: "1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: digest, RuntimeSpecSchemaVersion: "1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: digest, RuntimeProfileSchemaVersion: "1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	e, err := execution.New(tenant, eid, execution.Binding{SessionID: sid, SessionGeneration: 1, AuthorityMode: "LOCAL", AuthorityNamespace: "local", AuthorityReference: "grant", GrantDigest: digest, AgentID: "test", AgentVersionID: "1", AgentEvidence: []byte(`{}`), AgentEvidenceDigest: digest}, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	f := &executionFixture{fixture: &fixture{s: s}, e: e}
	denied, payloadDenied := false, false
	checks, payloads := 0, 0
	access := func(_ context.Context, _ app.Caller, sessionID primitives.ID, event *runtimeevent.Event) error {
		if sessionID != sid {
			t.Fatal("wrong Session")
		}
		if event != nil {
			payloads++
			if event.ExecutionID() != eid {
				t.Fatal("unrelated payload reached disclosure")
			}
			if payloadDenied {
				return errors.New("deny")
			}
		}
		return nil
	}
	execAccess := func(_ context.Context, _ app.Caller, sessionID, executionID primitives.ID) error {
		checks++
		if sessionID != sid || executionID != eid {
			t.Fatal("wrong binding")
		}
		if denied {
			return errors.New("deny")
		}
		return nil
	}
	reader, err := NewExecutionReader(f, clock.Fixed{Time: now}, access, execAccess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionReader(f, clock.UTC{}, access, nil); err != ErrUnavailable {
		t.Fatal("missing policy accepted")
	}
	caller := app.Caller{TenantID: tenant, PrincipalDigest: digest}
	for _, target := range []primitives.ID{"", other, eid} {
		event, err := runtimeevent.New(id(), tenant, sid, target, "", 2, 1, "session.degraded", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"reason":"fixture"}`), runtimeevent.Correlation{}, "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		f.read = persistence.EventRead{Earliest: 1, Latest: 2, Event: event}
		out, err := reader.Next(context.Background(), caller, eid, 1)
		if err != nil || out.Sequence != 2 || (len(out.Data) > 0) != (target == eid) {
			t.Fatal(out, err)
		}
	}
	if checks != 3 || payloads != 1 {
		t.Fatal(checks, payloads)
	}
	payloadDenied = true
	if out, err := reader.Next(context.Background(), caller, eid, 1); err != ErrNotFound || len(out.Data) > 0 {
		t.Fatal("payload denial bypass", err)
	}
	denied = true
	before := f.reads
	if _, err := reader.Next(context.Background(), caller, eid, 1); err != ErrNotFound || f.reads != before {
		t.Fatal("Execution denial bypass", err)
	}
	denied = false
	f.read = persistence.EventRead{Earliest: 3, Latest: 2}
	if _, err := reader.Next(context.Background(), caller, eid, 1); err != ErrGap {
		t.Fatal("retention bypass", err)
	}
	f.e = nil
	if _, err := reader.Next(context.Background(), caller, eid, 1); err != ErrNotFound {
		t.Fatal("missing Execution", err)
	}
}
