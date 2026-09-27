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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/config/sessionruntimes"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const createBody = `{"agent_runtime_spec_id":"approved-codex","runtime_profile_id":"coding-homelab-arm64","source":{"kind":"empty"}}`
const createKey = "session-create-test-001"

func sessionCatalog(t *testing.T) (*sessionruntimes.Catalog, *runtimeprofiles.Registry, sessionruntimes.Approved) {
	t.Helper()
	profile, err := os.ReadFile("../../../docs/profiles/coding-homelab-arm64.json")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := runtimeprofiles.New()
	if err != nil {
		t.Fatal(err)
	}
	if err = profiles.Reload([][]byte{profile}, func(runtimeprofile.Profile) ([]byte, error) {
		return []byte(`{"test_implementation":"qualified"}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema_version":1,"runtime_id":"codex-test","image":{"reference":"test.invalid/codex@sha256:` + strings.Repeat("a", 64) + `","digest":"sha256:` + strings.Repeat("a", 64) + `"},"adapter":{"kind":"codex","protocol_version":"1.0.0","compatibility":"1.0.0"},"entrypoint":{"command":["codex","app-server"]},"durable_vendor_paths":["/state/codex"],"workspace_mount":"/workspace","runtime_profile":{"name":"coding-homelab-arm64","minimum_isolation_class":"microvm-strong"},"platform":{"os":"linux","architectures":["arm64"]}}`)
	normalized, err := canonical.Transform(manifest)
	if err != nil {
		t.Fatal(err)
	}
	approved := sessionruntimes.Approved{ID: "approved-codex", AgentID: "codex", AgentVersionID: "test-v1", Digest: sandbox.Digest(normalized), PolicyDigest: sandbox.Digest([]byte("test-policy")), Manifest: manifest}
	catalog, err := sessionruntimes.NewLocal([]sessionruntimes.Approved{approved}, profiles, qualifyFixture)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, profiles, approved
}
func qualifyFixture(json.RawMessage, runtimeprofile.Profile) (json.RawMessage, error) {
	return json.RawMessage(`{"test_qualification":"fixture-only"}`), nil
}
func callerFixture(t *testing.T) sessions.Caller {
	t.Helper()
	id, err := primitives.NewID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return sessions.Caller{TenantID: id, PrincipalDigest: sandbox.Digest([]byte("test-issuer/test-subject"))}
}
func allowSession(context.Context, sessions.Caller, sessions.CreateRequest, primitives.ID) error {
	return nil
}
func sessionHandler(t *testing.T, c *sessions.Creator, a SessionAuthentication) stdhttp.Handler {
	t.Helper()
	s, err := NewServer(Options{Clock: clock.UTC{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sessions: c, AuthenticateSession: a})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}
func requestSession(h stdhttp.Handler, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type unavailableSessionStore struct{ calls atomic.Int32 }

func (s *unavailableSessionStore) WithinTransaction(context.Context, primitives.ID, func(context.Context, persistence.Repositories) error) error {
	s.calls.Add(1)
	return errors.New("private dependency diagnostic")
}

func TestCreateSessionRequestBoundary(t *testing.T) {
	catalog, _, _ := sessionCatalog(t)
	store := &unavailableSessionStore{}
	caller := callerFixture(t)
	creator, _ := sessions.NewCreator(store, catalog, clock.UTC{}, allowSession)
	auth := func(*stdhttp.Request) (sessions.Caller, error) { return caller, nil }
	h := sessionHandler(t, creator, auth)
	for _, body := range []string{
		`null`, `{}`, createBody + `{}`, strings.Replace(createBody, `"kind":"empty"`, `"kind":"empty","kind":"empty"`, 1),
		strings.Replace(createBody, `"kind":"empty"`, `"Kind":"empty"`, 1), strings.Replace(createBody, `"kind":"empty"`, `"kind":null`, 1),
		strings.Replace(createBody, `"source":`, `"tenant_id":"forged","source":`, 1), strings.Replace(createBody, `"source":`, `"Source":`, 1),
		strings.Replace(createBody, `"kind":"empty"`, `"kind":"empty","reference":null`, 1),
	} {
		if w := requestSession(h, body, createKey); w.Code != 400 {
			t.Errorf("invalid JSON got %d: %s", w.Code, w.Body)
		}
	}
	if w := requestSession(h, createBody, "short"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := requestSession(h, strings.Replace(createBody, `"empty"`, `"artifact"`, 1), createKey); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if store.calls.Load() != 0 {
		t.Fatal("invalid request reached storage")
	}
	if w := requestSession(sessionHandler(t, creator, nil), createBody, createKey); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := requestSession(h, createBody, createKey); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body)
	}
	for _, tc := range []struct {
		contentType string
		duplicate   bool
		status      int
	}{{"text/plain", false, 415}, {"application/json", true, 400}} {
		r := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(createBody))
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set("Idempotency-Key", createKey)
		if tc.duplicate {
			r.Header.Add("Idempotency-Key", createKey)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code)
		}
	}
}

func TestSessionRuntimeResolution(t *testing.T) {
	catalog, profiles, approved := sessionCatalog(t)
	r, err := catalog.Resolve(approved.ID, "coding-homelab-arm64")
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(r.Binding.RuntimeSpec)
	r.Binding.RuntimeSpec[0] = 'x'
	r.Implementation[0] = 'x'
	approved.Manifest[0] = 'x'
	again, _ := catalog.Resolve(approved.ID, "coding-homelab-arm64")
	if !bytes.Equal(again.Binding.RuntimeSpec, original) || again.Implementation[0] != '{' {
		t.Fatal("mutable resolution")
	}
	approved.Manifest = original
	if _, err = sessionruntimes.NewLocal([]sessionruntimes.Approved{approved}, profiles, nil); err == nil {
		t.Fatal("missing qualification accepted")
	}
	for _, mutation := range []func(*sessionruntimes.Approved){
		func(a *sessionruntimes.Approved) { a.Digest = sandbox.Digest([]byte("wrong")) },
		func(a *sessionruntimes.Approved) {
			a.Manifest = bytes.Replace(a.Manifest, []byte(`"architectures":["arm64"]`), []byte(`"architectures":["amd64"]`), 1)
			a.Digest = sandbox.Digest(a.Manifest)
		},
		func(a *sessionruntimes.Approved) {
			a.Manifest = bytes.Replace(a.Manifest, []byte(`/state/codex`), []byte(`/state/../secret`), 1)
			a.Digest = sandbox.Digest(a.Manifest)
		},
		func(a *sessionruntimes.Approved) {
			a.Manifest = bytes.Replace(a.Manifest, []byte(`"minimum_isolation_class":"microvm-strong"`), []byte(`"minimum_isolation_class":"confidential-strong"`), 1)
			a.Digest = sandbox.Digest(a.Manifest)
		},
		func(a *sessionruntimes.Approved) {
			a.Manifest = bytes.Replace(a.Manifest, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
			a.Digest = sandbox.Digest(a.Manifest)
		},
	} {
		a := approved
		a.Manifest = bytes.Clone(a.Manifest)
		mutation(&a)
		if _, err = sessionruntimes.NewLocal([]sessionruntimes.Approved{a}, profiles, qualifyFixture); err == nil {
			t.Fatal("invalid runtime accepted")
		}
	}
	if _, err = catalog.Resolve("missing", "coding-homelab-arm64"); err == nil {
		t.Fatal("unknown runtime accepted")
	}
}

// This test exercises the public route over a real TCP connection, real
// PostgreSQL transactions and concurrent callers. Authentication is explicitly
// a test double; no development bypass is added to the executable.
func TestPostgresCreateSessionHTTP(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	caller := callerFixture(t)
	other := callerFixture(t)
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
	var deny atomic.Bool
	access := func(ctx context.Context, c sessions.Caller, r sessions.CreateRequest, id primitives.ID) error {
		if deny.Load() && id != "" {
			return sessions.ErrForbidden
		}
		return nil
	}
	creator, err := sessions.NewCreator(store, catalog, clock.UTC{}, access)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(r *stdhttp.Request) (sessions.Caller, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer test-a":
			return caller, nil
		case "Bearer test-b":
			return other, nil
		default:
			return sessions.Caller{}, errors.New("unauthenticated")
		}
	}
	server := httptest.NewServer(sessionHandler(t, creator, auth))
	defer server.Close()
	send := func(body, key, token string) (int, stdhttp.Header, []byte, error) {
		r, _ := stdhttp.NewRequest("POST", server.URL+"/v1/sessions", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(r)
		if err != nil {
			return 0, nil, nil, err
		}
		defer response.Body.Close()
		bodyBytes, err := io.ReadAll(response.Body)
		return response.StatusCode, response.Header, bodyBytes, err
	}
	const concurrency = 10
	results := make(chan []byte, concurrency)
	failures := make(chan error, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			status, _, body, err := send(createBody, createKey, "test-a")
			if err != nil || status != 201 {
				failures <- errors.New(string(body))
				return
			}
			results <- body
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var original []byte
	for body := range results {
		if original == nil {
			original = body
		} else if !bytes.Equal(original, body) {
			t.Fatal("concurrent request created a second result")
		}
	}
	var view sessions.View
	if err = json.Unmarshal(original, &view); err != nil || view.State != "PROVISIONING" || view.StateVersion != 0 || view.AuthorityMode != "local" || view.WorkspaceID == "" {
		t.Fatal(string(original), err)
	}
	normalized := `{ "source": {"kind":"empty"}, "runtime_profile_id":"coding-homelab-arm64", "agent_runtime_spec_id":"approved-codex" }`
	status, headers, body, err := send(normalized, createKey, "test-a")
	if err != nil || status != 201 || headers.Get("Idempotency-Replayed") != "true" || !bytes.Equal(body, original) || headers.Get("Location") != "/v1/sessions/"+string(view.ID) {
		t.Fatal(status, string(body), err)
	}
	status, _, _, _ = send(strings.Replace(createBody, "approved-codex", "other-runtime", 1), createKey, "test-a")
	if status != 409 {
		t.Fatal("conflict", status)
	}
	status, _, body, _ = send(createBody, createKey, "test-b")
	var otherView sessions.View
	_ = json.Unmarshal(body, &otherView)
	if status != 201 || otherView.ID == view.ID {
		t.Fatal("cross tenant collision")
	}
	deny.Store(true)
	status, _, _, _ = send(createBody, createKey, "test-a")
	if status != 403 {
		t.Fatal("replay disclosed after revocation", status)
	}
	deny.Store(false)
	// Reconstruct the application and replay without resolving the old runtime.
	_, profiles, approved := sessionCatalog(t)
	approved.ID = "replacement-only"
	replacement, err := sessionruntimes.NewLocal([]sessionruntimes.Approved{approved}, profiles, qualifyFixture)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := sessions.NewCreator(store, replacement, clock.UTC{}, access)
	w := requestSession(sessionHandler(t, restarted, func(*stdhttp.Request) (sessions.Caller, error) { return caller, nil }), createBody, createKey)
	if w.Code != 201 || !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatal("restart replay", w.Code, w.Body)
	}
	// Failed outbox persistence rolls back Session, event and replay reservation.
	failing, _ := sessions.NewCreator(failingOutboxStore{store}, catalog, clock.UTC{}, access)
	w = requestSession(sessionHandler(t, failing, func(*stdhttp.Request) (sessions.Caller, error) { return caller, nil }), createBody, "rollback-create-key")
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	err = store.WithinTransaction(context.Background(), caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		s, e := repos.Sessions().Get(ctx, view.ID)
		if e != nil {
			return e
		}
		if s.Binding().AgentID != "codex" || s.StateVersion() != 0 {
			return errors.New("incorrect binding")
		}
		if _, e = repos.Sessions().Get(ctx, otherView.ID); !errors.Is(e, persistence.ErrNotFound) {
			return errors.New("tenant isolation failed")
		}
		// Generic expiry must not remove a resource-creating identity.
		deleted, e := repos.Idempotency().DeleteExpired(ctx, time.Now().AddDate(2, 0, 0), 100)
		if e != nil || deleted != 0 {
			return errors.New("Session identity expired")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "runtime_events", "idempotency_records", "outbox_messages"} {
		var n int
		if err = tx.QueryRow(`SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, caller.TenantID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s count %d: %v", table, n, err)
		}
	}
	var intentRaw []byte
	if err = tx.QueryRow(`SELECT payload FROM outbox_messages WHERE tenant_id=$1`, caller.TenantID).Scan(&intentRaw); err != nil {
		t.Fatal(err)
	}
	var intent sessions.ProvisioningIntent
	if json.Unmarshal(intentRaw, &intent) != nil || intent.WorkspaceID != view.WorkspaceID || intent.SessionID != view.ID || intent.SourceOperationID == "" || intent.WorkspaceOperationID == "" || intent.PolicyDigest == "" {
		t.Fatal("incomplete provisioning intent")
	}
}

type failingOutboxStore struct{ persistence.TransactionManager }

func (s failingOutboxStore) WithinTransaction(ctx context.Context, tenant primitives.ID, work func(context.Context, persistence.Repositories) error) error {
	return s.TransactionManager.WithinTransaction(ctx, tenant, func(ctx context.Context, r persistence.Repositories) error { return work(ctx, failingRepositories{r}) })
}

type failingRepositories struct{ persistence.Repositories }

func (r failingRepositories) Outbox() persistence.OutboxRepository {
	return failingOutbox{r.Repositories.Outbox()}
}

type failingOutbox struct{ persistence.OutboxRepository }

func (failingOutbox) Add(context.Context, *outbox.Message) error {
	return errors.New("test outbox failure")
}
