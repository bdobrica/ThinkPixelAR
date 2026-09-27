package eventstream

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type fixture struct {
	persistence.Repositories
	persistence.SessionRepository
	persistence.RuntimeEventRepository
	s     *session.Session
	read  persistence.EventRead
	reads int
}

func (f *fixture) WithinTransaction(ctx context.Context, _ primitives.ID, fn func(context.Context, persistence.Repositories) error) error {
	return fn(ctx, f)
}
func (f *fixture) Sessions() persistence.SessionRepository                      { return f }
func (f *fixture) RuntimeEvents() persistence.RuntimeEventRepository            { return f }
func (f *fixture) Get(context.Context, primitives.ID) (*session.Session, error) { return f.s, nil }
func (f *fixture) ReadNext(context.Context, primitives.ID, uint64, time.Time) (persistence.EventRead, error) {
	f.reads++
	return f.read, nil
}
func TestReadGapAndDisclosure(t *testing.T) {
	now := time.Now().UTC()
	tenant, _ := primitives.NewID(now)
	sid, _ := primitives.NewID(now)
	eid, _ := primitives.NewID(now)
	digest := sandbox.Digest(nil)
	binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", AgentID: "test", AgentVersionID: "1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: digest, RuntimeSpecSchemaVersion: "1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: digest, RuntimeProfileSchemaVersion: "1"}
	sess, err := session.New(tenant, sid, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := runtimeevent.New(eid, tenant, sid, "", "", 2, 1, "session.degraded", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Confidential, []byte(`{"reason":"private"}`), runtimeevent.Correlation{}, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: sess}
	caller := app.Caller{TenantID: tenant, PrincipalDigest: digest}
	denySession, denyEvent := false, false
	reader, _ := NewReader(f, clock.Fixed{Time: now}, func(_ context.Context, _ app.Caller, _ primitives.ID, e *runtimeevent.Event) error {
		if denySession || (denyEvent && e != nil) {
			return errors.New("denied")
		}
		return nil
	})
	for _, tc := range []struct {
		name  string
		after uint64
		read  persistence.EventRead
		want  error
	}{
		{"empty", 0, persistence.EventRead{Earliest: 1}, nil},
		{"caught up", 2, persistence.EventRead{Earliest: 1, Latest: 2}, nil},
		{"prefix expired", 0, persistence.EventRead{Earliest: 2, Latest: 2, Event: event}, ErrGap},
		{"all expired", 1, persistence.EventRead{Earliest: 3, Latest: 2}, ErrGap},
		{"interior hole", 0, persistence.EventRead{Earliest: 1, Latest: 2, Event: event}, ErrGap},
		{"tail expired", 1, persistence.EventRead{Earliest: 1, Latest: 2}, ErrGap},
		{"future", 3, persistence.EventRead{Earliest: 1, Latest: 2}, ErrInvalid},
		{"resume", 1, persistence.EventRead{Earliest: 2, Latest: 2, Event: event}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.read = tc.read
			out, err := reader.Next(context.Background(), caller, sid, tc.after)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if err != nil && len(out.Data) != 0 {
				t.Fatal("error disclosed content")
			}
		})
	}
	f.read = persistence.EventRead{Earliest: 1, Latest: 2, Event: event}
	denyEvent = true
	if out, err := reader.Next(context.Background(), caller, sid, 1); err != ErrNotFound || len(out.Data) != 0 {
		t.Fatal("event policy bypass")
	}
	denySession = true
	before := f.reads
	if _, err := reader.Next(context.Background(), caller, sid, 1); err != ErrNotFound || f.reads != before {
		t.Fatal("session policy bypass")
	}
}
