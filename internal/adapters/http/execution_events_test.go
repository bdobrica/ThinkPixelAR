package http

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/eventstream"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Uses two real generations in one Session, including cancelled historical
// authority, from TestPostgresCreateExecutionHTTP. Disclosure is independent.
func testExecutionSSE(t *testing.T, store persistence.TransactionManager, caller, other sessions.Caller, sid, eid primitives.ID) {
	var deny atomic.Bool
	access := func(_ context.Context, _ sessions.Caller, id primitives.ID, e *runtimeevent.Event) error {
		if id != sid {
			return errors.New("denied")
		}
		if e != nil && e.ExecutionID() != eid {
			return errors.New("wrong Execution")
		}
		return nil // explicit fixture payload permission
	}
	execAccess := func(_ context.Context, _ sessions.Caller, s, e primitives.ID) error {
		if deny.Load() || s != sid || e != eid {
			return errors.New("denied")
		}
		return nil
	}
	reader, err := eventstream.NewExecutionReader(store, clock.UTC{}, access, execAccess)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(r *stdhttp.Request) (sessions.Caller, error) {
		if r.Header.Get("Authorization") == "other" {
			return other, nil
		}
		return caller, nil
	}
	// Exercise the production route and shared transport through HTTP/2.
	adapter, err := NewServer(Options{Clock: clock.UTC{}, ExecutionEvents: reader, AuthenticateSession: auth})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(adapter.Handler())
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	server.Client().Timeout = 3 * time.Second
	open := func(after, token string) *stdhttp.Response {
		t.Helper()
		r, _ := stdhttp.NewRequest("GET", server.URL+"/v1/executions/"+string(eid)+"/events/stream", nil)
		if after != "" {
			r.Header.Set("Last-Event-ID", after)
		}
		r.Header.Set("Authorization", token)
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	frame := func(r *bufio.Reader) string {
		t.Helper()
		var out strings.Builder
		for {
			s, err := r.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			out.WriteString(s)
			if s == "\n" {
				return out.String()
			}
		}
	}
	check := func(f string) uint64 {
		t.Helper()
		parts := strings.SplitN(f, "data: ", 2)
		if len(parts) != 2 {
			t.Fatal(f)
		}
		var e struct {
			Execution primitives.ID `json:"execution_id"`
			Sequence  uint64        `json:"sequence"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &e) != nil || e.Execution != eid || !strings.HasPrefix(f, fmt.Sprintf("id: %d\n", e.Sequence)) {
			t.Fatal(f)
		}
		return e.Sequence
	}
	resp := open("", "caller")
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 {
		t.Fatal(resp.StatusCode)
	}
	stream := bufio.NewReader(resp.Body)
	frame(stream)
	if seq := check(frame(stream)); seq != 2 {
		t.Fatal("lost Session sequence", seq)
	}
	resp.Body.Close()
	appendEvent := func(target primitives.ID, expired bool) uint64 {
		t.Helper()
		var seq uint64
		err := store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
			var err error
			seq, err = r.RuntimeEvents().NextSequence(ctx, sid)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			id, _ := primitives.NewID(now)
			var until *time.Time
			if expired {
				v := now.Add(-time.Minute)
				until = &v
			}
			e, err := runtimeevent.New(id, caller.TenantID, sid, target, "", seq, 1, "session.degraded", now.Add(-time.Hour), now.Add(-time.Hour), runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"reason":"fixture"}`), runtimeevent.Correlation{}, "test", until)
			if err != nil {
				return err
			}
			return r.RuntimeEvents().Append(ctx, e)
		})
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	// Skip Session-only events and the later Execution's events without exposing
	// their data or interpreting their nonconsecutive visible IDs as loss.
	last := appendEvent(eid, false)
	resp = open("2", "caller")
	stream = bufio.NewReader(resp.Body)
	frame(stream)
	for {
		if check(frame(stream)) == last {
			break
		}
	}
	live := appendEvent(eid, false)
	if got := check(frame(stream)); got != live {
		t.Fatal(got, live)
	}
	deny.Store(true)
	if f := frame(stream); !strings.Contains(f, "access-ended") {
		t.Fatal(f)
	}
	if raw, err := io.ReadAll(stream); err != nil || len(raw) != 0 {
		t.Fatal("stream did not end", err)
	}
	resp.Body.Close()
	for _, tc := range []struct {
		after, token string
		status       int
	}{{"0", "caller", 404}, {"0", "other", 404}} {
		r := open(tc.after, tc.token)
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Fatal(r.StatusCode)
		}
	}
	deny.Store(false)
	for _, tc := range []struct {
		after  string
		status int
	}{{"01", 400}, {"999999", 400}, {"2", 200}} {
		r := open(tc.after, "caller")
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Fatal(r.StatusCode)
		}
	}
	appendEvent("", true)
	appendEvent(eid, false)
	resp = open(strconv.FormatUint(live, 10), "caller")
	defer resp.Body.Close()
	if resp.StatusCode != 410 {
		t.Fatal("missing retention gap", resp.StatusCode)
	}
}
