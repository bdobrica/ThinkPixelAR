package http

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type statusStore struct {
	persistence.Repositories
	persistence.ExecutionRepository
	e      *domain.Execution
	err    error
	tenant primitives.ID
}

func (s *statusStore) WithinTransaction(ctx context.Context, tenant primitives.ID, fn func(context.Context, persistence.Repositories) error) error {
	s.tenant = tenant
	if s.err != nil {
		return s.err
	}
	return fn(ctx, s)
}
func (s *statusStore) Executions() persistence.ExecutionRepository                   { return s }
func (s *statusStore) Get(context.Context, primitives.ID) (*domain.Execution, error) { return s.e, nil }

func TestExecutionStatusHTTP(t *testing.T) {
	caller := callerFixture(t)
	id, _ := primitives.NewID(time.Now())
	sid, _ := primitives.NewID(time.Now())
	now := time.Now().Add(-24 * time.Hour)
	b := domain.Binding{SessionID: sid, SessionGeneration: 2, AuthorityMode: "THINKPIXEL_AG", AuthorityNamespace: "ag-issuer", AuthorityReference: "private-grant", ExternalRunID: "run-123", GrantDigest: sandbox.Digest(nil), AgentID: "agent", AgentVersionID: "version", AgentEvidence: []byte(`{"private":"evidence"}`), AgentEvidenceDigest: sandbox.Digest(nil)}
	store := &statusStore{}
	denied := false
	calls := 0
	reader, err := executions.NewReader(store, func(_ context.Context, c executions.Caller, sessionID, executionID primitives.ID) error {
		calls++
		if c != caller || sessionID != sid || executionID != id {
			t.Fatal("wrong authorization identity")
		}
		if denied {
			return errors.New("private policy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := func(*stdhttp.Request) (executions.Caller, error) { return caller, nil }
	handler := executionHandler(t, nil, auth, reader)
	for _, state := range []domain.State{domain.Queued, domain.Materializing, domain.Running, domain.Cancelling, domain.TimingOut, domain.Succeeded, domain.Failed, domain.Cancelled, domain.TimedOut} {
		var result *domain.TerminalResult
		var terminal *time.Time
		switch state {
		case domain.Succeeded, domain.Failed, domain.Cancelled, domain.TimedOut:
			result = &domain.TerminalResult{Reference: "private-result", Digest: sandbox.Digest(nil)}
			terminal = &now
		}
		store.e, err = domain.Restore(caller.TenantID, id, b, now.Add(time.Hour), state, 7, result, now, now, terminal)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/executions/"+string(id), nil))
		var got executions.Status
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.State != state || got.StateVersion != 7 || got.RunReference != "run-123" || got.AuthorityMode != "thinkpixelag" || got.Generation != 2 || store.tenant != caller.TenantID {
			t.Fatal(w.Code, w.Body.String())
		}
		var fields map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &fields)
		if len(fields) != 9 {
			t.Fatal(w.Body.String())
		}
	}
	if calls != 9 {
		t.Fatal(calls)
	}
	for _, tc := range []struct {
		name    string
		auth    SessionAuthentication
		reader  *executions.Reader
		path    string
		deny    bool
		failure error
		status  int
	}{
		{"unauthenticated", nil, reader, string(id), false, nil, 401},
		{"authentication failure", func(*stdhttp.Request) (executions.Caller, error) { return caller, errors.New("private") }, reader, string(id), false, nil, 401},
		{"unconfigured", auth, nil, string(id), false, nil, 503},
		{"invalid id", auth, reader, "invalid", false, nil, 400},
		{"query", auth, reader, string(id) + "?tenant=other", false, nil, 400},
		{"denied", auth, reader, string(id), true, nil, 404},
		{"missing", auth, reader, string(id), false, persistence.ErrNotFound, 404},
		{"database", auth, reader, string(id), false, errors.New("private database"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denied = tc.deny
			store.err = tc.failure
			w := httptest.NewRecorder()
			executionHandler(t, nil, tc.auth, tc.reader).ServeHTTP(w, httptest.NewRequest("GET", "/v1/executions/"+tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
