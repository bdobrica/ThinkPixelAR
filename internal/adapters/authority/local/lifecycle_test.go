package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLifecycleUnavailable(t *testing.T) {
	c, registry, s, caller, r, now := fixture(t)
	a, _ := New(c, registry, unavailable{}, clock.Fixed{Time: now})
	g, _ := a.evaluate(caller, r, s, s.ID(), now)
	if status, err := a.Validate(context.Background(), caller, g); err != authority.ErrUnavailable || status.State == authority.Active {
		t.Fatalf("%+v %v", status, err)
	}
	if err := a.Cancel(context.Background(), authority.Caller{}, g); err != authority.ErrDenied {
		t.Fatal(err)
	}
}

func TestPostgresImmutableGrantLifecycle(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	c, registry, s, caller, r, now := fixture(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tenants(tenant_id) VALUES($1)`, caller.TenantID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error { return repos.Sessions().Add(ctx, s) }); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	a, err := New(c, registry, store, clock.Fixed{Time: now}, Observability{Logger: telemetry.NewJSONLogger(&logs, telemetry.LogOptions{})})
	if err != nil {
		t.Fatal(err)
	}
	g, err := a.Admit(ctx, caller, r)
	if err != nil {
		t.Fatal(err)
	}
	check := func(a *Authority, g authority.Grant, want authority.State) {
		t.Helper()
		status, e := a.Validate(ctx, caller, g)
		if e != nil || status.State != want {
			t.Fatalf("status %+v %v; want %s", status, e, want)
		}
	}
	check(a, g, authority.Active)
	if !strings.Contains(logs.String(), `"authority_state":"ACTIVE"`) || !strings.Contains(logs.String(), `"operation":"admit","result":"success"`) {
		t.Fatalf("missing success telemetry: %s", &logs)
	}
	otherCaller := caller
	otherCaller.TenantID = g.ID
	otherGrant := g
	otherGrant.TenantID = g.ID
	if _, e := a.Validate(ctx, otherCaller, otherGrant); e != authority.ErrInvalidGrant {
		t.Fatalf("tenant isolation: %v", e)
	}
	unknown := g
	unknown.ID = g.SessionID
	if _, e := a.Validate(ctx, caller, unknown); e != authority.ErrInvalidGrant {
		t.Fatalf("unknown grant: %v", e)
	}
	early, _ := New(c, registry, store, clock.Fixed{Time: now.Add(-time.Nanosecond)})
	if _, e := early.Validate(ctx, caller, g); e != authority.ErrInvalidGrant {
		t.Fatalf("before issuance: %v", e)
	}
	// Copy all nested state before each tamper; every authority-bearing field
	// remains part of the exact stored snapshot, including issuance evidence.
	raw, _ := json.Marshal(g)
	for name, mutate := range map[string]func(*authority.Grant){
		"deadline":       func(g *authority.Grant) { g.ExpiresAt = g.ExpiresAt.Add(time.Hour) },
		"resource":       func(g *authority.Grant) { g.Profile.Resources.CPU.Limit++ },
		"network":        func(g *authority.Grant) { g.Profile.Network.Profile = "none" },
		"runtime":        func(g *authority.Grant) { g.Runtime.RuntimeSpec[0] = 'x' },
		"implementation": func(g *authority.Grant) { g.Implementation[0] = 'x' },
		"capability":     func(g *authority.Grant) { g.Capabilities = []string{"shell"} },
		"policy":         func(g *authority.Grant) { g.PolicyDigest = sandbox.Digest([]byte("other")) },
		"generation":     func(g *authority.Grant) { g.Generation++ },
		"session":        func(g *authority.Grant) { g.SessionID = g.ID },
		"request":        func(g *authority.Grant) { g.RequestDigest = sandbox.Digest([]byte("other")) },
		"principal":      func(g *authority.Grant) { g.PrincipalDigest = sandbox.Digest([]byte("other")) },
		"tenant":         func(g *authority.Grant) { g.TenantID = g.ID },
	} {
		t.Run(name, func(t *testing.T) {
			var altered authority.Grant
			_ = json.Unmarshal(raw, &altered)
			mutate(&altered)
			if _, e := a.Validate(ctx, caller, altered); e != authority.ErrInvalidGrant {
				t.Fatal(e)
			}
			if e := a.Cancel(ctx, caller, altered); e != authority.ErrInvalidGrant {
				t.Fatal(e)
			}
		})
	}
	check(a, g, authority.Active)
	// Direct SQL cannot mutate the immutable snapshot, even alongside a valid
	// status transition. The adapter has no corresponding update operation.
	for _, query := range []string{
		`UPDATE local_authority_grants SET snapshot=convert_to('{}','UTF8'),state='CANCELLED',state_version=1,terminal_at=now() WHERE tenant_id=$1 AND grant_id=$2`,
		`UPDATE local_authority_grants SET snapshot_digest=$3,state='CANCELLED',state_version=1,terminal_at=now() WHERE tenant_id=$1 AND grant_id=$2`,
	} {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID)
		if e != nil {
			t.Fatal(e)
		}
		args := []any{caller.TenantID, g.ID}
		if strings.Contains(query, "$3") {
			args = append(args, sandbox.Digest([]byte("other")))
		}
		_, e = tx.ExecContext(ctx, query, args...)
		_ = tx.Rollback()
		var pgErr *pgconn.PgError
		if !errors.As(e, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("expected immutable check violation, got %v", e)
		}
	}
	// Concurrent cancellation is idempotent and survives adapter/store restart.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() { errs[i] = a.Cancel(ctx, caller, g) })
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	check(a, g, authority.Cancelled)
	c.Revision = sandbox.Digest([]byte("new revision"))
	store, _ = postgres.NewStore(db)
	restarted, err := New(c, registry, store, clock.Fixed{Time: now})
	if err != nil {
		t.Fatal(err)
	}
	check(restarted, g, authority.Cancelled)
	replay, err := restarted.Admit(ctx, caller, r)
	if err != nil || !reflect.DeepEqual(replay, g) {
		t.Fatal("cancelled replay changed snapshot", err)
	}
	// Expiry is inclusive, persisted, and cannot be undone by clock rollback.
	r.KeyDigest = sandbox.Digest([]byte("expiry"))
	exp, err := a.Admit(ctx, caller, r)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := New(c, registry, store, clock.Fixed{Time: exp.ExpiresAt.Add(-time.Nanosecond)})
	check(before, exp, authority.Active)
	at, _ := New(c, registry, store, clock.Fixed{Time: exp.ExpiresAt})
	check(at, exp, authority.Expired)
	check(a, exp, authority.Expired)
	if err = at.Cancel(ctx, caller, exp); err != nil {
		t.Fatal(err)
	}
	// Concurrent expiry and cancellation elect one irreversible status.
	r.KeyDigest = sandbox.Digest([]byte("race"))
	raced, e := a.Admit(ctx, caller, r)
	if e != nil {
		t.Fatal(e)
	}
	for i := range errs {
		wg.Go(func() {
			if i%2 == 0 {
				errs[i] = a.Cancel(ctx, caller, raced)
			} else {
				_, errs[i] = at.Validate(ctx, caller, raced)
			}
		})
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	winner, e := a.Validate(ctx, caller, raced)
	if e != nil || (winner.State != authority.Cancelled && winner.State != authority.Expired) {
		t.Fatalf("race: %+v %v", winner, e)
	}
	check(at, raced, winner.State)
	// Replay cleanup does not delete authority evidence or resurrect cancellation.
	if err = store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		_, e := repos.Idempotency().DeleteExpired(ctx, now.Add(48*time.Hour), 100)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	check(restarted, g, authority.Cancelled)
}
