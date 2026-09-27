package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func signalHandler(t *testing.T, s *executions.Signaler, auth SessionAuthentication) stdhttp.Handler {
	t.Helper()
	server, err := NewServer(Options{ExecutionSignals: s, AuthenticateSession: auth, Clock: clock.UTC{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}
func TestSignalRequestBoundary(t *testing.T) {
	store := &unavailableSessionStore{}
	s, _ := executions.NewLocalSignaler(store, clock.UTC{}, func(context.Context, executions.Caller, primitives.ID, primitives.ID) error { return nil }, func(context.Context, persistence.Repositories, executions.Caller, *domain.Execution, executions.SignalRequest) error {
		return nil
	})
	caller := callerFixture(t)
	auth := func(*stdhttp.Request) (executions.Caller, error) { return caller, nil }
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"type":"user_input","payload":{"input":"hi"}}`, 503},
		{`{"type":"interrupt","payload":{}}`, 503},
		{`{"type":"permission_response","payload":{}}`, 503},
		{`{"type":"user_input","payload":null}`, 400},
		{`{"type":"cancel","payload":{}}`, 400},
		{`{"type":"user_input","payload":{"a":1,"a":2}}`, 400},
		{`{"type":"interrupt","payload":{},"tenant_id":"x"}`, 400},
		{`{"Type":"interrupt","payload":{}}`, 400},
		{`{"type":"user_input","payload":[]}`, 400},
		{`{"type":"interrupt","payload":{}}{}`, 400},
		{strings.Repeat("x", executions.MaxSignalBytes+1), 413},
	} {
		r := httptest.NewRequest("POST", "/v1/executions/"+string(caller.TenantID)+"/signals", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", createKey)
		w := httptest.NewRecorder()
		signalHandler(t, s, auth).ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		s    *executions.Signaler
		auth SessionAuthentication
		want int
	}{{s, nil, 401}, {nil, auth, 503}} {
		w := httptest.NewRecorder()
		signalHandler(t, tc.s, tc.auth).ServeHTTP(w, httptest.NewRequest("POST", "/v1/executions/"+string(caller.TenantID)+"/signals", nil))
		if w.Code != tc.want {
			t.Fatal(w.Code)
		}
	}
}

// Extends the real HTTP/PostgreSQL admission fixture. Lifecycle transitions here
// are explicit fixtures, not claims that materialization/delivery is composed.
func exerciseSignals(t *testing.T, store *postgres.Store, db *sql.DB, caller, other executions.Caller, eid primitives.ID) func() {
	t.Helper()
	var denied atomic.Bool
	var policyCalls atomic.Int32
	access := func(context.Context, executions.Caller, primitives.ID, primitives.ID) error {
		if denied.Load() {
			return errors.New("private denied")
		}
		return nil
	}
	policy := func(_ context.Context, _ persistence.Repositories, _ executions.Caller, _ *domain.Execution, r executions.SignalRequest) error {
		policyCalls.Add(1)
		if r.Type != "user_input" {
			return executions.ErrUnsupported
		}
		var p map[string]string
		if json.Unmarshal(r.Payload, &p) != nil || len(p) != 1 || p["input"] == "" {
			return executions.ErrInvalid
		}
		return nil
	}
	s, _ := executions.NewLocalSignaler(store, clock.UTC{}, access, policy)
	auth := func(r *stdhttp.Request) (executions.Caller, error) {
		if r.Header.Get("Authorization") == "other" {
			return other, nil
		}
		return caller, nil
	}
	server := httptest.NewServer(signalHandler(t, s, auth))
	t.Cleanup(server.Close)
	send := func(body, key, token string) (int, stdhttp.Header, []byte) {
		req, _ := stdhttp.NewRequest("POST", server.URL+"/v1/executions/"+string(eid)+"/signals", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", token)
		resp, err := server.Client().Do(req)
		if err != nil {
			return 0, nil, []byte(err.Error())
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, raw
	}
	const body = `{"type":"user_input","payload":{"input":"private-signal-input"}}`
	transition := func(state domain.State) {
		t.Helper()
		err := store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
			e, err := r.Executions().GetForUpdate(ctx, eid)
			if err != nil {
				return err
			}
			v := e.StateVersion()
			if err = e.Transition(state, v, nil, time.Now()); err != nil {
				return err
			}
			return r.Executions().Update(ctx, e, v)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []domain.State{domain.Queued, domain.Materializing} {
		if state == domain.Materializing {
			transition(state)
		}
		code, _, raw := send(body, createKey, "")
		if code != 409 {
			t.Fatal(state, code, string(raw))
		}
	}
	transition(domain.Running)
	if code, _, _ := send(body, createKey, "other"); code != 404 {
		t.Fatal("tenant", code)
	}
	denied.Store(true)
	if code, _, _ := send(body, createKey, ""); code != 404 {
		t.Fatal("access", code)
	}
	denied.Store(false)
	// Concurrency converges on one private payload/event/outbox and one policy call.
	var wg sync.WaitGroup
	results := make(chan []byte, 8)
	for range 8 {
		wg.Go(func() {
			code, _, raw := send(body, createKey, "")
			if code != 202 {
				results <- nil
			} else {
				results <- raw
			}
		})
	}
	wg.Wait()
	close(results)
	var original []byte
	for raw := range results {
		if raw == nil {
			t.Fatal("concurrent signal rejected")
		}
		if original == nil {
			original = raw
		} else if !bytes.Equal(original, raw) {
			t.Fatal("different signal operations")
		}
	}
	if policyCalls.Load() != 1 {
		t.Fatal("replay reran policy", policyCalls.Load())
	}
	var op executions.SignalOperation
	if json.Unmarshal(original, &op) != nil || op.State != "PENDING" || op.Kind != "execution.signal" {
		t.Fatal(string(original))
	}
	code, headers, raw := send(`{ "payload": {"input":"private-signal-input"}, "type":"user_input" }`, createKey, "")
	if code != 202 || headers.Get("Idempotency-Replayed") != "true" || !bytes.Equal(raw, original) {
		t.Fatal("normalized replay", code, string(raw))
	}
	if code, _, _ := send(`{"type":"user_input","payload":{"input":"changed"}}`, createKey, ""); code != 409 {
		t.Fatal("conflict", code)
	}
	if code, _, _ := send(`{"type":"interrupt","payload":{}}`, createKey+"-unsupported", ""); code != 422 {
		t.Fatal("unsupported", code)
	}
	if code, _, _ := send(`{"type":"user_input","payload":{}}`, createKey+"-invalid", ""); code != 400 {
		t.Fatal("invalid payload", code)
	}
	denied.Store(true)
	if code, _, _ := send(body, createKey, ""); code != 404 {
		t.Fatal("replay disclosure", code)
	}
	denied.Store(false)
	// Failure after body/event insertion must roll back the entire operation.
	broken, _ := executions.NewLocalSignaler(failingOutboxStore{store}, clock.UTC{}, access, policy)
	request, _ := executions.ParseSignal([]byte(body))
	if _, err := broken.Signal(context.Background(), caller, eid, createKey+"-rollback", request); !errors.Is(err, executions.ErrUnavailable) {
		t.Fatal("rollback", err)
	}
	expired, _ := executions.NewLocalSignaler(store, clock.Fixed{Time: time.Now().Add(2 * time.Hour)}, access, policy)
	if _, err := expired.Signal(context.Background(), caller, eid, createKey+"-expired", request); !errors.Is(err, executions.ErrDenied) {
		t.Fatal("expiry", err)
	}
	if err := store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		v, err := r.Executions().GetSignal(ctx, op.ID)
		if err != nil {
			return err
		}
		if v.ExecutionID != eid || !bytes.Contains(v.Payload, []byte("private-signal-input")) {
			return errors.New("signal body missing")
		}
		_, err = r.Idempotency().DeleteExpired(ctx, time.Now().AddDate(2, 0, 0), 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(context.Background(), other.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		_, err := r.Executions().GetSignal(ctx, op.ID)
		if errors.Is(err, persistence.ErrNotFound) {
			return nil
		}
		return errors.New("signal tenant leak")
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM execution_signals WHERE tenant_id=$1 AND execution_id=$2`,
		`SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND execution_id=$2 AND event_type='signal.accepted'`,
		`SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND topic='execution.signal.v1' AND payload->>'execution_id'=$2`,
		`SELECT count(*) FROM idempotency_records WHERE tenant_id=$1 AND action='executions.signal.v1' AND resource_id=$2`,
	} {
		var count int
		if err = tx.QueryRow(query, caller.TenantID, eid).Scan(&count); err != nil || count != 1 {
			t.Fatal("signal atomicity", count, err)
		}
	}
	var leaks int
	if err = tx.QueryRow(`SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND payload::text LIKE '%private-signal-input%'`, caller.TenantID).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatal("private signal in outbox", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Fresh service, expired clock and later terminal state can replay only the
	// original acceptance. A new key must still validate lifecycle/authority.
	return func() {
		t.Helper()
		replay, err := expired.Signal(context.Background(), caller, eid, createKey, request)
		if err != nil || !replay.Replayed || replay.Operation != op {
			t.Fatal("durable signal replay", err)
		}
		if _, err = s.Signal(context.Background(), caller, eid, createKey+"-after", request); !errors.Is(err, executions.ErrConflict) && !errors.Is(err, executions.ErrDenied) {
			t.Fatal("new signal after revocation/terminal", err)
		}
	}
}
