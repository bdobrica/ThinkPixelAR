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
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/authority/local"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type unusedAdmission struct{}

func (unusedAdmission) AdmitInTransaction(context.Context, persistence.Repositories, authority.Caller, authority.Request) (authority.Grant, error) {
	return authority.Grant{}, authority.ErrUnavailable
}
func allowExecution(context.Context, executions.Caller, primitives.ID, executions.CreateRequest) error {
	return nil
}
func executionHandler(t *testing.T, c *executions.Creator, auth SessionAuthentication, readers ...*executions.Reader) stdhttp.Handler {
	t.Helper()
	var reader *executions.Reader
	if len(readers) > 0 {
		reader = readers[0]
	}
	s, err := NewServer(Options{ExecutionReader: reader, Clock: clock.UTC{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Executions: c, AuthenticateSession: auth})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}
func TestCreateExecutionRequestBoundary(t *testing.T) {
	store := &unavailableSessionStore{}
	c, err := executions.NewLocalCreator(store, unusedAdmission{}, clock.UTC{}, allowExecution)
	if err != nil {
		t.Fatal(err)
	}
	caller := callerFixture(t)
	h := executionHandler(t, c, func(*stdhttp.Request) (sessions.Caller, error) { return caller, nil })
	for _, tc := range []struct {
		name, body, tag, key string
		status               int
	}{
		{"valid", `{"input":"hello"}`, `"1"`, createKey, 503},
		{"duplicate", `{"input":"a","input":"b"}`, `"1"`, createKey, 400},
		{"null", `{"input":null}`, `"1"`, createKey, 400},
		{"alias", `{"Input":"hello"}`, `"1"`, createKey, 400},
		{"identity", `{"input":"hello","tenant_id":"other"}`, `"1"`, createKey, 400},
		{"empty", `{"input":""}`, `"1"`, createKey, 400},
		{"trailing", `{"input":"hi"}{}`, `"1"`, createKey, 400},
		{"run", `{"input":"hi","run_reference":"ag-run"}`, `"1"`, createKey, 422},
		{"missing version", `{"input":"hi"}`, "", createKey, 400},
		{"weak version", `{"input":"hi"}`, `W/"1"`, createKey, 400},
		{"zero version", `{"input":"hi"}`, `"0"`, createKey, 400},
		{"padded version", `{"input":"hi"}`, `"01"`, createKey, 400},
		{"short key", `{"input":"hi"}`, `"1"`, "short", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := store.calls.Load()
			r := httptest.NewRequest("POST", "/v1/sessions/"+string(caller.TenantID)+"/executions", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("If-Match", tc.tag)
			r.Header.Set("Idempotency-Key", tc.key)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.status != 503 && before != store.calls.Load() {
				t.Fatal("invalid request reached persistence")
			}
		})
	}
	r := httptest.NewRequest("POST", "/v1/sessions/"+string(caller.TenantID)+"/executions", nil)
	w := httptest.NewRecorder()
	executionHandler(t, c, nil).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

// Real TCP HTTP + PostgreSQL + LocalAuthority; identity/qualification are explicit
// fixtures. A READY Session is seeded because Workspace provisioning is separate.
func TestPostgresCreateExecutionHTTP(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	caller, other := callerFixture(t), callerFixture(t)
	for _, c := range []sessions.Caller{caller, other} {
		tx, err := db.Begin()
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
	catalog, registry, _ := sessionCatalog(t)
	resolution, err := catalog.Resolve("approved-codex", "coding-homelab-arm64")
	if err != nil {
		t.Fatal(err)
	}
	p, _, _, _, _, _ := registry.Lookup("coding-homelab-arm64")
	a, err := local.New(local.Config{Mode: "local", Revision: sandbox.Digest([]byte("test-policy")), DefaultProfile: p.Name, Profiles: []string{p.Name}, DefaultDuration: time.Minute, MaximumDuration: time.Hour, CPU: p.Resources.CPU.Limit, Memory: p.Resources.Memory.Limit, EphemeralStorage: p.Resources.EphemeralStorage.Limit, WorkspaceBytes: p.Storage.WorkspaceBytes, MaxProcesses: p.Resources.MaxProcesses, Architectures: []string{"arm64"}, Networks: []string{p.Network.Profile}, Runtimes: []local.ApprovedRuntime{{Binding: resolution.Binding, Profiles: []string{p.Name}}}}, registry, store, clock.UTC{})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(c sessions.Caller) primitives.ID {
		t.Helper()
		id, _ := primitives.NewID(time.Now())
		s, err := session.New(c.TenantID, id, resolution.Binding, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Transition(session.Ready, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = store.WithinTransaction(context.Background(), c.TenantID, func(ctx context.Context, r persistence.Repositories) error { return r.Sessions().Add(ctx, s) }); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sid, second, otherSID := seed(caller), seed(caller), seed(other)
	var deny atomic.Bool
	access := func(context.Context, executions.Caller, primitives.ID, executions.CreateRequest) error {
		if deny.Load() {
			return errors.New("revoked")
		}
		return nil
	}
	creator, err := executions.NewLocalCreator(store, a, clock.UTC{}, access)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(r *stdhttp.Request) (sessions.Caller, error) {
		if r.Header.Get("Authorization") == "Bearer other" {
			return other, nil
		}
		return caller, nil
	}
	reader, err := executions.NewReader(store, func(ctx context.Context, c executions.Caller, sessionID, executionID primitives.ID) error {
		if sessionID == "" || executionID == "" {
			t.Fatal("missing disclosure identity")
		}
		return access(ctx, c, sessionID, executions.CreateRequest{})
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(executionHandler(t, creator, auth, reader))
	defer server.Close()
	send := func(id primitives.ID, body, key, token string) (int, stdhttp.Header, []byte, error) {
		r, _ := stdhttp.NewRequest("POST", server.URL+"/v1/sessions/"+string(id)+"/executions", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", `"1"`)
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(r)
		if err != nil {
			return 0, nil, nil, err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		return response.StatusCode, response.Header, raw, err
	}
	const body = `{"input":"confidential-execution-input"}`
	const n = 10
	results := make(chan []byte, n)
	failures := make(chan string, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			status, _, raw, err := send(sid, body, createKey, "caller")
			if err != nil || status != 201 {
				failures <- string(raw)
				return
			}
			results <- raw
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	var original []byte
	for raw := range results {
		if original == nil {
			original = raw
		} else if !bytes.Equal(original, raw) {
			t.Fatal("duplicate created another Execution")
		}
	}
	var view executions.View
	if json.Unmarshal(original, &view) != nil || view.State != "QUEUED" || view.StateVersion != 0 || view.Generation != 1 || view.AuthorityMode != "local" {
		t.Fatal(string(original))
	}
	get := func(id primitives.ID, token string, want int, state string, generation uint64) {
		t.Helper()
		req, _ := stdhttp.NewRequest("GET", server.URL+"/v1/executions/"+string(id), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(resp.StatusCode, string(raw))
		}
		if want == 200 {
			var got executions.Status
			if json.Unmarshal(raw, &got) != nil || got.ID != id || got.SessionID != sid || string(got.State) != state || got.Generation != generation || got.AuthorityMode != "local" || got.AuthorityIssuer != authority.LocalIssuer {
				t.Fatal(string(raw))
			}
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			if len(fields) != 8 {
				t.Fatal("unexpected public fields", string(raw))
			}
		}
	}
	get(view.ID, "caller", 200, "QUEUED", 1)
	get(view.ID, "other", 404, "", 0)
	get(caller.TenantID, "caller", 404, "", 0)
	deny.Store(true)
	get(view.ID, "caller", 404, "", 0)
	deny.Store(false)
	status, headers, raw, err := send(sid, `{ "input" : "confidential-execution-input", "run_reference":"" }`, createKey, "caller")
	if err != nil || status != 201 || headers.Get("Idempotency-Replayed") != "true" || headers.Get("Location") != "/v1/executions/"+string(view.ID) || !bytes.Equal(raw, original) {
		t.Fatal(status, string(raw), err)
	}
	for _, tc := range []struct {
		id               primitives.ID
		body, key, token string
		status           int
	}{
		{sid, `{"input":"changed"}`, createKey, "caller", 409},
		{sid, body, createKey + "-new", "caller", 409},
		{sid, body, createKey, "other", 404},
		{otherSID, body, createKey, "other", 201},
	} {
		status, _, raw, err = send(tc.id, tc.body, tc.key, tc.token)
		if err != nil || status != tc.status {
			t.Fatal(status, string(raw), err)
		}
	}
	deny.Store(true)
	status, _, _, _ = send(sid, body, createKey, "caller")
	if status != 404 {
		t.Fatal("replay authorization", status)
	}
	deny.Store(false)
	// Restarted service can replay without consulting authority, even after expiry.
	restarted, _ := executions.NewLocalCreator(store, unusedAdmission{}, clock.Fixed{Time: time.Now().Add(2 * time.Hour)}, access)
	replay, err := restarted.Create(context.Background(), caller, sid, 1, createKey, executions.CreateRequest{Input: "confidential-execution-input"})
	if err != nil || !replay.Replayed || replay.View.ID != view.ID {
		t.Fatal("restart replay", err)
	}
	// Outbox failure rolls back grant, input, generation, events and both replay records.
	broken, _ := executions.NewLocalCreator(failingOutboxStore{store}, a, clock.UTC{}, access)
	if _, err = broken.Create(context.Background(), caller, second, 1, createKey, executions.CreateRequest{Input: "rollback"}); !errors.Is(err, executions.ErrUnavailable) {
		t.Fatal(err)
	}
	err = store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		s, e := r.Sessions().Get(ctx, sid)
		if e != nil {
			return e
		}
		if s.State() != session.Active || s.ExecutionGeneration() != 1 || s.StateVersion() != 2 {
			return errors.New("Session fence mismatch")
		}
		s, e = r.Sessions().Get(ctx, second)
		if e != nil {
			return e
		}
		if s.State() != session.Ready || s.ExecutionGeneration() != 0 {
			return errors.New("rollback changed Session")
		}
		ex, bound, e := executions.LoadLocalBinding(ctx, r, view.ID)
		if e != nil {
			return e
		}
		grant, e := r.LocalGrants().Get(ctx, primitives.ID(ex.Binding().AuthorityReference))
		if e != nil {
			return e
		}
		if grant.Digest != ex.Binding().GrantDigest || grant.SessionID != sid || bound.ID != grant.ID || bound.Runtime.AgentVersionID != resolution.Binding.AgentVersionID {
			return errors.New("grant binding mismatch")
		}
		input, digest, e := r.Executions().GetInput(ctx, view.ID)
		if e != nil {
			return e
		}
		if input != "confidential-execution-input" || digest != sandbox.Digest([]byte(input)) {
			return errors.New("input mismatch")
		}
		_, e = r.Idempotency().DeleteExpired(ctx, time.Now().AddDate(2, 0, 0), 100)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Create(context.Background(), caller, sid, 1, createKey, executions.CreateRequest{Input: "confidential-execution-input"}); err != nil {
		t.Fatal("key retention", err)
	}
	err = store.WithinTransaction(context.Background(), other.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		if _, _, e := r.Executions().GetInput(ctx, view.ID); !errors.Is(e, persistence.ErrNotFound) {
			return errors.New("cross-tenant input disclosure")
		}
		return nil
	})
	if err != nil {
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
	for table, want := range map[string]int{"executions": 1, "execution_inputs": 1, "local_authority_grants": 1, "runtime_events": 2, "outbox_messages": 1, "idempotency_records": 1} {
		var count int
		if err = tx.QueryRow(`SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, caller.TenantID).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count %d want %d: %v", table, count, want, err)
		}
	}
	var current string
	if err = tx.QueryRow(`SELECT current_execution_id FROM sessions WHERE tenant_id=$1 AND session_id=$2`, caller.TenantID, sid).Scan(&current); err != nil || current != string(view.ID) {
		t.Fatal("current writer", err)
	}
	var payload []byte
	if err = tx.QueryRow(`SELECT payload FROM outbox_messages WHERE tenant_id=$1`, caller.TenantID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var intent executions.QueueIntent
	if json.Unmarshal(payload, &intent) != nil || intent.ExecutionID != view.ID || intent.GrantID == "" || intent.InputDigest != sandbox.Digest([]byte("confidential-execution-input")) || bytes.Contains(payload, []byte("confidential-execution-input")) {
		t.Fatal("queue intent")
	}
	// Competing distinct keys must elect one writer, not just deduplicate a key.
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	const contenders = 256
	conflicts := make(chan int, contenders)
	start := make(chan struct{})
	for i := range contenders {
		wg.Go(func() {
			<-start
			status, _, _, err := send(second, body, createKey+"-"+strconv.Itoa(i), "caller")
			if err != nil {
				conflicts <- 0
				return
			}
			if status == 201 {
				wins.Add(1)
			} else {
				conflicts <- status
			}
		})
	}
	close(start)
	wg.Wait()
	close(conflicts)
	if wins.Load() != 1 {
		t.Fatal("writer winners", wins.Load())
	}
	for status := range conflicts {
		if status != 409 {
			t.Fatal("competing writer", status)
		}
	}
	if err = store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		s, e := r.Sessions().Get(ctx, second)
		if e != nil {
			return e
		}
		if s.State() != session.Active || s.ExecutionGeneration() != 1 || s.StateVersion() != 2 {
			return errors.New("competing admission advanced twice")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	verifySignalReplay := exerciseSignals(t, store, db, caller, other, view.ID)

	// Reconstruct using a new store as after process replacement. No catalog or
	// admission participates; cancellation changes status, never bound history.
	reopened, _ := postgres.NewStore(db)
	load := func() authority.Grant {
		t.Helper()
		var grant authority.Grant
		if err := reopened.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
			_, g, err := executions.LoadLocalBinding(ctx, r, view.ID)
			grant = g
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return grant
	}
	originalGrant := load()
	originalSnapshot, _ := json.Marshal(originalGrant)
	changed := load()
	changed.Runtime.RuntimeSpec[0] = '!'
	changed.Profile.Resources.CPU.Limit++
	unchanged, _ := json.Marshal(load())
	if !bytes.Equal(originalSnapshot, unchanged) {
		t.Fatal("reader mutated durable grant")
	}
	ac := authority.Caller{TenantID: caller.TenantID, PrincipalDigest: caller.PrincipalDigest}
	if status, err := a.Validate(context.Background(), ac, originalGrant); err != nil || status.State != authority.Active {
		t.Fatal(status, err)
	}
	if err := a.Cancel(context.Background(), ac, originalGrant); err != nil {
		t.Fatal(err)
	}
	verifySignalReplay()
	unchanged, _ = json.Marshal(load())
	if !bytes.Equal(originalSnapshot, unchanged) {
		t.Fatal("cancellation rewrote binding")
	}
	if status, err := a.Validate(context.Background(), ac, load()); err != nil || status.State != authority.Cancelled {
		t.Fatal(status, err)
	}
	if err := reopened.WithinTransaction(context.Background(), other.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		_, _, err := executions.LoadLocalBinding(ctx, r, view.ID)
		if !errors.Is(err, persistence.ErrNotFound) {
			return errors.New("cross-tenant binding disclosure")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Existing database invariants prevent in-place grant/runtime/deadline edits.
	for _, mutation := range []string{
		"grant_digest='sha256:' || repeat('0',64)",
		"agent_version_id='replacement'",
		"agent_evidence='{}'::jsonb",
		"deadline=deadline+interval '1 second'",
	} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(`UPDATE executions SET `+mutation+` WHERE tenant_id=$1 AND execution_id=$2`, caller.TenantID, view.ID)
		_ = tx.Rollback()
		if err == nil {
			t.Fatal("database allowed immutable binding edit", mutation)
		}
	}
	// Finish the seeded operation and admit a later generation. Historical
	// evidence must still load without pretending its old grant authorizes work.
	// Terminal service composition is separate work; seed its durable result.
	finished, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer finished.Rollback()
	if _, err = finished.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = finished.Exec(`UPDATE executions SET state='FAILED',state_version=state_version+1,terminal_result_reference='test:finished',terminal_result_digest=$3,terminal_at=now(),updated_at=now() WHERE tenant_id=$1 AND execution_id=$2`, caller.TenantID, view.ID, sandbox.Digest([]byte("finished"))); err != nil {
		t.Fatal(err)
	}
	if _, err = finished.Exec(`UPDATE sessions SET state='IDLE',state_version=state_version+1,current_execution_id=NULL,updated_at=now() WHERE tenant_id=$1 AND session_id=$2`, caller.TenantID, sid); err != nil {
		t.Fatal(err)
	}
	if err = finished.Commit(); err != nil {
		t.Fatal(err)
	}
	later, err := creator.Create(context.Background(), caller, sid, 3, createKey+"-later", executions.CreateRequest{Input: "next operation"})
	if err != nil || later.View.Generation != 2 {
		t.Fatal("later admission", err)
	}
	get(view.ID, "caller", 200, "FAILED", 1)
	get(later.View.ID, "caller", 200, "QUEUED", 2)
	verifySignalReplay()
	unchanged, _ = json.Marshal(load())
	if !bytes.Equal(originalSnapshot, unchanged) {
		t.Fatal("later generation rewrote history")
	}

	t.Run("ExecutionSSE", func(t *testing.T) { testExecutionSSE(t, store, caller, other, sid, view.ID) })

}
