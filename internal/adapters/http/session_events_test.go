package http

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/app/eventstream"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestSessionStreamBoundary(t *testing.T) {
	caller := callerFixture(t)
	reader, _ := eventstream.NewReader(&unavailableSessionStore{}, clock.UTC{}, func(context.Context, sessions.Caller, primitives.ID, *runtimeevent.Event) error { return nil })
	auth := func(*stdhttp.Request) (sessions.Caller, error) { return caller, nil }
	s, _ := NewServer(Options{Clock: clock.UTC{}, SessionEvents: reader, AuthenticateSession: auth})
	for _, value := range []string{"-1", "+1", "01", " 1", "1,2", "9223372036854775808", "x", ""} {
		r := httptest.NewRequest("GET", "/v1/sessions/"+string(caller.TenantID)+"/events/stream", nil)
		r.Header["Last-Event-Id"] = []string{value}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%q: %d", value, w.Code)
		}
	}
	for _, value := range []string{"0", "1", "9223372036854775807"} {
		if _, err := parseEventSequence(value, nil); err != nil {
			t.Fatal(err)
		}
	}
	limits := &streamLimits{}
	releases := []func(){}
	for range 4 {
		release, ok := limits.acquire("tenant", "principal", "ip")
		if !ok {
			t.Fatal("early quota")
		}
		releases = append(releases, release)
	}
	if _, ok := limits.acquire("tenant", "principal", "ip"); ok {
		t.Fatal("quota bypass")
	}
	for _, release := range releases {
		release()
	}
	if limits.total != 0 || len(limits.principals) != 0 || len(limits.tenants) != 0 || len(limits.ips) != 0 {
		t.Fatal("quota leak")
	}
}

func TestPostgresSessionStreamHTTP(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	caller, other := callerFixture(t), callerFixture(t)
	for _, c := range []sessions.Caller{caller, other} {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, c.TenantID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO tenants(tenant_id) VALUES($1)`, c.TenantID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := postgres.NewStore(db)
	catalog, _, _ := sessionCatalog(t)
	creator, _ := sessions.NewCreator(store, catalog, clock.UTC{}, func(context.Context, sessions.Caller, sessions.CreateRequest, primitives.ID) error { return nil })
	created, err := creator.Create(context.Background(), caller, createKey, sessions.CreateRequest{RuntimeID: "approved-codex", ProfileID: "coding-homelab-arm64", Source: sessions.Source{Kind: "empty"}})
	if err != nil {
		t.Fatal(err)
	}
	view := created.View
	var deny, expired atomic.Bool
	access := func(_ context.Context, _ sessions.Caller, _ primitives.ID, e *runtimeevent.Event) error {
		if deny.Load() {
			return errors.New("private denial")
		}
		// Trusted fixture permits only these two closed metadata payloads.
		if e != nil && e.Type() != "session.created" && e.Type() != "session.degraded" {
			return errors.New("unapproved event")
		}
		return nil
	}
	auth := func(r *stdhttp.Request) (sessions.Caller, error) {
		if expired.Load() {
			return sessions.Caller{}, errors.New("expired")
		}
		if r.Header.Get("Authorization") == "other" {
			return other, nil
		}
		return caller, nil
	}
	reader, _ := eventstream.NewReader(store, clock.UTC{}, access)
	timing := streamTiming{10 * time.Millisecond, 150 * time.Millisecond, 100 * time.Millisecond, 2 * time.Second}
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", sessionStream(reader, auth, &streamLimits{}, timing))
	server := httptest.NewServer(mux)
	defer server.Close()
	server.Client().Timeout = 3 * time.Second
	open := func(after, token string) *stdhttp.Response {
		t.Helper()
		r, _ := stdhttp.NewRequest("GET", server.URL+"/v1/sessions/"+string(view.ID)+"/events/stream", nil)
		if after != "" {
			r.Header.Set("Last-Event-ID", after)
		}
		r.Header.Set("Authorization", token)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	frame := func(r *bufio.Reader) string {
		t.Helper()
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(line)
			if line == "\n" {
				return b.String()
			}
		}
	}
	response := open("", "caller")
	if response.StatusCode != 200 || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal(response.StatusCode)
	}
	stream := bufio.NewReader(response.Body)
	if frame(stream) != "retry: 1000\n\n" {
		t.Fatal("missing retry")
	}
	first := frame(stream)
	if !strings.HasPrefix(first, "id: 1\nevent: session.created\ndata: ") {
		t.Fatal(first)
	}
	var envelope map[string]json.RawMessage
	raw := strings.TrimSuffix(strings.SplitN(first, "data: ", 2)[1], "\n\n")
	if json.Unmarshal([]byte(raw), &envelope) != nil || len(envelope) != 13 {
		t.Fatal(raw)
	}
	if frame(stream) != ": heartbeat\n\n" {
		t.Fatal("missing idle heartbeat")
	}
	appendEvent := func(until *time.Time) uint64 {
		t.Helper()
		var seq uint64
		err := store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
			seq, err = repos.RuntimeEvents().NextSequence(ctx, view.ID)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			occurred := now.Add(-time.Hour)
			id, _ := primitives.NewID(now)
			e, err := runtimeevent.New(id, caller.TenantID, view.ID, "", "", seq, 1, "session.degraded", occurred, occurred, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"reason":"fixture"}`), runtimeevent.Correlation{}, "test", until)
			if err != nil {
				return err
			}
			return repos.RuntimeEvents().Append(ctx, e)
		})
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	appendEvent(nil)
	for {
		f := frame(stream)
		if strings.HasPrefix(f, "id: 2\n") {
			break
		}
		if f != ": heartbeat\n\n" {
			t.Fatal(f)
		}
	}
	response.Body.Close()
	// A fresh reader and HTTP server resume using PostgreSQL alone.
	reader, _ = eventstream.NewReader(store, clock.UTC{}, access)
	replacementMux := stdhttp.NewServeMux()
	replacementMux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", sessionStream(reader, auth, &streamLimits{}, timing))
	replacement := httptest.NewUnstartedServer(replacementMux)
	replacement.EnableHTTP2 = true
	replacement.StartTLS()
	defer replacement.Close()
	server = replacement
	server.Client().Timeout = 3 * time.Second
	response = open("1", "caller")
	if response.ProtoMajor != 2 {
		t.Fatal("HTTP/2 not exercised")
	}
	stream = bufio.NewReader(response.Body)
	frame(stream)
	if f := frame(stream); !strings.HasPrefix(f, "id: 2\n") {
		t.Fatal(f)
	}
	if frame(stream) != ": heartbeat\n\n" {
		t.Fatal("idle HTTP/2 deadline ended stream")
	}
	deny.Store(true)
	remaining, err := io.ReadAll(stream)
	response.Body.Close()
	if err != nil || !strings.Contains(string(remaining), "access-ended") || strings.Contains(string(remaining), "data:") {
		t.Fatal(string(remaining), err)
	}
	response = open("2", "caller")
	if response.StatusCode != 404 {
		t.Fatal(response.StatusCode)
	}
	response.Body.Close()
	deny.Store(false)
	response = open("1", "other")
	if response.StatusCode != 404 {
		t.Fatal("tenant disclosure", response.StatusCode)
	}
	response.Body.Close()
	response = open("99", "caller")
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	response.Body.Close()
	// An interior expiry must not silently skip to the later retained event.
	until := time.Now().Add(-time.Minute)
	appendEvent(&until)
	appendEvent(nil)
	response = open("2", "caller")
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 410 || !strings.Contains(string(body), `"latest_sequence":4`) || strings.Contains(string(body), "fixture") {
		t.Fatal(response.StatusCode, string(body))
	}
	response = open("4", "caller")
	stream = bufio.NewReader(response.Body)
	frame(stream)
	expired.Store(true)
	remaining, err = io.ReadAll(stream)
	response.Body.Close()
	if err != nil || !strings.Contains(string(remaining), "access-ended") {
		t.Fatal(string(remaining), err)
	}
	expired.Store(false)
	// A stalled ResponseWriter must be interrupted by the configured deadline,
	// and release its connection quota without retaining a database transaction.
	limits := &streamLimits{}
	stalled := &stalledStreamWriter{header: stdhttp.Header{}}
	request := httptest.NewRequest("GET", "/v1/sessions/"+string(view.ID)+"/events/stream", nil)
	request.SetPathValue("session_id", string(view.ID))
	request.Header.Set("Last-Event-ID", "4")
	started := time.Now()
	sessionStream(reader, auth, limits, streamTiming{time.Millisecond, time.Millisecond, 20 * time.Millisecond, time.Second})(stalled, request)
	if elapsed := time.Since(started); elapsed > time.Second || elapsed < 15*time.Millisecond || limits.total != 0 || stalled.writes != 1 {
		t.Fatal("write deadline/quota failure", elapsed, limits.total, stalled.writes)
	}

	// Bound the lifetime even for an authorized idle consumer.
	shortMux := stdhttp.NewServeMux()
	shortMux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", sessionStream(reader, auth, &streamLimits{}, streamTiming{time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}))
	short := httptest.NewServer(shortMux)
	defer short.Close()
	r, _ := stdhttp.NewRequest("GET", fmt.Sprintf("%s/v1/sessions/%s/events/stream", short.URL, view.ID), nil)
	r.Header.Set("Last-Event-ID", "4")
	resp, err := short.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// Simulates a socket that cannot accept any more bytes. Unlike a recorder it
// requires the handler to set a deadline to make progress.
type stalledStreamWriter struct {
	header   stdhttp.Header
	deadline time.Time
	writes   int
}

func (w *stalledStreamWriter) Header() stdhttp.Header              { return w.header }
func (w *stalledStreamWriter) WriteHeader(int)                     {}
func (w *stalledStreamWriter) SetWriteDeadline(at time.Time) error { w.deadline = at; return nil }
func (w *stalledStreamWriter) FlushError() error                   { return nil }
func (w *stalledStreamWriter) Write([]byte) (int, error) {
	w.writes++
	if w.deadline.IsZero() {
		return 0, errors.New("missing deadline")
	}
	time.Sleep(time.Until(w.deadline))
	return 0, os.ErrDeadlineExceeded
}
