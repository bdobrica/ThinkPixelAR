package local

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type unavailable struct{}

func (unavailable) WithinTransaction(context.Context, primitives.ID, func(context.Context, persistence.Repositories) error) error {
	return errors.New("sensitive database detail")
}

func fixture(t *testing.T) (Config, *runtimeprofiles.Registry, *session.Session, authority.Caller, authority.Request, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenant, _ := primitives.NewID(now)
	sid, _ := primitives.NewID(now)
	registry, e := runtimeprofiles.New()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../../../docs/profiles/coding-medium-secure.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = registry.Reload([][]byte{raw}, func(runtimeprofile.Profile) ([]byte, error) { return []byte(`{"qualified":"fixture"}`), nil }); e != nil {
		t.Fatal(e)
	}
	p, canonical, pdigest, _, _, _ := registry.Lookup("coding-medium-secure")
	spec := []byte(`{"approved_runtime":"fixture"}`)
	binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "1", RuntimeSpec: spec, RuntimeSpecDigest: sandbox.Digest(spec), RuntimeProfileSchemaVersion: "1", RuntimeProfileSnapshot: canonical, RuntimeProfileDigest: pdigest}
	s, e := session.New(tenant, sid, binding, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Transition(session.Ready, 0, now); e != nil {
		t.Fatal(e)
	}
	c := Config{Mode: "local", Revision: sandbox.Digest([]byte("revision")), DefaultProfile: p.Name, Profiles: []string{p.Name}, DefaultDuration: time.Minute, MaximumDuration: time.Hour, CPU: p.Resources.CPU.Limit, Memory: p.Resources.Memory.Limit, EphemeralStorage: p.Resources.EphemeralStorage.Limit, WorkspaceBytes: p.Storage.WorkspaceBytes, MaxProcesses: p.Resources.MaxProcesses, Architectures: []string{"amd64"}, Networks: []string{p.Network.Profile}, Runtimes: []ApprovedRuntime{{Binding: binding, Profiles: []string{p.Name}, Capabilities: []string{"shell", "process"}}}}
	caller := authority.Caller{TenantID: tenant, PrincipalDigest: sandbox.Digest([]byte("principal"))}
	request := authority.Request{SessionID: sid, SessionVersion: 1, Generation: 1, KeyDigest: sandbox.Digest([]byte("key")), RequestDigest: sandbox.Digest([]byte("input"))}
	return c, registry, s, caller, request, now
}
func TestPolicyBounds(t *testing.T) {
	c, registry, s, caller, r, now := fixture(t)
	a, e := New(c, registry, unavailable{}, clock.Fixed{Time: now})
	if e != nil {
		t.Fatal(e)
	}
	id, _ := primitives.NewID(now)
	g, e := a.evaluate(caller, r, s, id, now)
	if e != nil || g.Mode != "local" || g.Issuer != "thinkpixelar/local" || g.Generation != 1 || !g.ExpiresAt.Equal(now.Add(time.Minute)) || g.PolicyDigest == c.Revision || g.Profile.Network.Profile != "thinkpixel-only" {
		t.Fatalf("default: %+v %v", g, e)
	}
	cap := int64(100)
	caller.Deadline = now.Add(30 * time.Second)
	r.CPU = &runtimeprofile.RequestLimit{Request: 100, Limit: 200}
	r.WorkspaceBytes = &cap
	r.Capabilities = []string{"shell"}
	g, e = a.evaluate(caller, r, s, id, now)
	if e != nil || !g.ExpiresAt.Equal(caller.Deadline) || g.Profile.Resources.CPU.Limit != 200 || g.Profile.Storage.WorkspaceBytes != 100 {
		t.Fatalf("narrowing: %v", e)
	}
	// Mutating returned or original operator data cannot widen another issuance.
	g.Profile.Platform.Architectures[0] = "arm64"
	c.Networks[0] = "unrestricted-standalone"
	c.Runtimes[0].Binding.RuntimeSpec[0] = 'x'
	r.CPU = nil
	r.WorkspaceBytes = nil
	g, e = a.evaluate(caller, r, s, id, now)
	if e != nil || g.Profile.Platform.Architectures[0] != "amd64" || g.Profile.Resources.CPU.Limit != 4000 {
		t.Fatal("policy alias")
	}
}
func TestDeniedRequests(t *testing.T) {
	cases := map[string]func(*authority.Caller, *authority.Request){
		"profile":           func(_ *authority.Caller, r *authority.Request) { r.Profile = "unknown" },
		"network":           func(_ *authority.Caller, r *authority.Request) { r.Network = "unrestricted-standalone" },
		"architecture":      func(_ *authority.Caller, r *authority.Request) { r.Architecture = "arm64" },
		"duration":          func(_ *authority.Caller, r *authority.Request) { r.Duration = 2 * time.Hour },
		"negative duration": func(_ *authority.Caller, r *authority.Request) { r.Duration = -1 },
		"cpu": func(_ *authority.Caller, r *authority.Request) {
			r.CPU = &runtimeprofile.RequestLimit{Request: 2000, Limit: 4001}
		},
		"memory": func(_ *authority.Caller, r *authority.Request) {
			r.Memory = &runtimeprofile.RequestLimit{Request: 1, Limit: 1 << 40}
		},
		"ephemeral": func(_ *authority.Caller, r *authority.Request) {
			r.EphemeralStorage = &runtimeprofile.RequestLimit{Request: 1, Limit: 1 << 40}
		},
		"zero": func(_ *authority.Caller, r *authority.Request) { r.CPU = &runtimeprofile.RequestLimit{} },
		"contradiction": func(_ *authority.Caller, r *authority.Request) {
			r.CPU = &runtimeprofile.RequestLimit{Request: 2, Limit: 1}
		},
		"workspace":            func(_ *authority.Caller, r *authority.Request) { v := int64(1 << 40); r.WorkspaceBytes = &v },
		"processes":            func(_ *authority.Caller, r *authority.Request) { v := int64(1025); r.MaxProcesses = &v },
		"capability":           func(_ *authority.Caller, r *authority.Request) { r.Capabilities = []string{"enterprise-tool"} },
		"duplicate capability": func(_ *authority.Caller, r *authority.Request) { r.Capabilities = []string{"shell", "shell"} },
		"version":              func(_ *authority.Caller, r *authority.Request) { r.SessionVersion++ },
		"generation":           func(_ *authority.Caller, r *authority.Request) { r.Generation++ },
		"tenant":               func(c *authority.Caller, _ *authority.Request) { c.TenantID, _ = primitives.NewID(time.Now()) },
		"expired":              func(c *authority.Caller, _ *authority.Request) { c.Deadline = time.Unix(1, 0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, registry, s, caller, r, now := fixture(t)
			a, e := New(c, registry, unavailable{}, clock.Fixed{Time: now})
			if e != nil {
				t.Fatal(e)
			}
			mutate(&caller, &r)
			if _, e = a.evaluate(caller, r, s, r.SessionID, now); !errors.Is(e, authority.ErrDenied) {
				t.Fatal(e)
			}
		})
	}
}
func TestInvalidConfigurationAndAuthentication(t *testing.T) {
	cases := map[string]func(*Config){
		"no mode": func(c *Config) { c.Mode = "" }, "AG no fallback": func(c *Config) { c.Mode = "thinkpixelag" },
		"duration": func(c *Config) { c.DefaultDuration = 0 }, "unbounded": func(c *Config) { c.MaximumDuration = 0 },
		"unknown profile": func(c *Config) { c.Profiles = []string{"unknown"}; c.DefaultProfile = "unknown" },
		"default":         func(c *Config) { c.DefaultProfile = "unknown" }, "global cpu": func(c *Config) { c.CPU = 1 },
		"global memory": func(c *Config) { c.Memory = 1 }, "global ephemeral": func(c *Config) { c.EphemeralStorage = 1 },
		"global storage": func(c *Config) { c.WorkspaceBytes = 1 }, "global processes": func(c *Config) { c.MaxProcesses = 1 },
		"network": func(c *Config) { c.Networks = []string{"none"} }, "architecture": func(c *Config) { c.Architectures = []string{"arm64"} },
		"capabilities": func(c *Config) { c.Runtimes[0].Capabilities = []string{"unknown"} }, "runtime integrity": func(c *Config) { c.Runtimes[0].Binding.RuntimeSpec = []byte(`{}`) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, registry, _, _, _, now := fixture(t)
			mutate(&c)
			if _, e := New(c, registry, unavailable{}, clock.Fixed{Time: now}); !errors.Is(e, ErrConfiguration) {
				t.Fatal(e)
			}
		})
	}
	c, registry, _, caller, r, now := fixture(t)
	a, _ := New(c, registry, unavailable{}, clock.Fixed{Time: now})
	if _, e := a.Admit(context.Background(), authority.Caller{}, r); !errors.Is(e, authority.ErrDenied) {
		t.Fatal(e)
	}
	if _, e := a.Admit(context.Background(), caller, r); e != authority.ErrUnavailable {
		t.Fatal(e)
	}
	c.Principals = []string{sandbox.Digest([]byte("someone else"))}
	a, _ = New(c, registry, unavailable{}, clock.Fixed{Time: now})
	if _, e := a.Admit(context.Background(), caller, r); e != authority.ErrDenied {
		t.Fatal(e)
	}
}

func TestPostgresAdmissionReplay(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	c, registry, s, caller, r, now := fixture(t)
	db, e := sql.Open("pgx", url)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, caller.TenantID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO tenants(tenant_id) VALUES($1)`, caller.TenantID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	store, _ := postgres.NewStore(db)
	if e = store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error { return repos.Sessions().Add(ctx, s) }); e != nil {
		t.Fatal(e)
	}
	a, e := New(c, registry, store, clock.Fixed{Time: now})
	if e != nil {
		t.Fatal(e)
	}
	const count = 8
	grants := make([]authority.Grant, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() { grants[i], errs[i] = a.Admit(ctx, caller, r) })
	}
	wg.Wait()
	for i := range count {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if !reflect.DeepEqual(grants[i], grants[0]) {
			t.Fatal("concurrent replay changed grant")
		}
	}
	// A fresh adapter/store and later clock return the exact original expired grant.
	store, _ = postgres.NewStore(db)
	c.Revision = sandbox.Digest([]byte("new policy"))
	a, e = New(c, registry, store, clock.Fixed{Time: now.Add(2 * time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := a.Admit(ctx, caller, r)
	if e != nil || !reflect.DeepEqual(replay, grants[0]) {
		t.Fatalf("restart replay: %v", e)
	}
	r.Duration = 2 * time.Minute // same caller-provided digest cannot disguise changed constraints
	if _, e = a.Admit(ctx, caller, r); e != authority.ErrConflict {
		t.Fatalf("conflict: %v", e)
	}
	r.KeyDigest = sandbox.Digest([]byte("denied"))
	r.Network = "unrestricted-standalone"
	if _, e = a.Admit(ctx, caller, r); e != authority.ErrDenied {
		t.Fatal(e)
	}
	r.Network = "" // denial rolls back reservation so a corrected request can be admitted
	if _, e = a.Admit(ctx, caller, r); e != nil {
		t.Fatal(e)
	}
}

func TestSessionRuntimeAndLifecycleBinding(t *testing.T) {
	for _, change := range []string{"active", "agent-version", "runtime-digest", "profile-digest"} {
		t.Run(change, func(t *testing.T) {
			c, registry, s, caller, r, now := fixture(t)
			a, e := New(c, registry, unavailable{}, clock.Fixed{Time: now})
			if e != nil {
				t.Fatal(e)
			}
			if change == "active" {
				if e = s.Transition(session.Active, 1, now); e != nil {
					t.Fatal(e)
				}
				r.SessionVersion = s.StateVersion()
				r.Generation = s.ExecutionGeneration() + 1
			} else {
				b := s.Binding()
				switch change {
				case "agent-version":
					b.AgentVersionID = "unapproved"
				case "runtime-digest":
					b.RuntimeSpecDigest = sandbox.Digest([]byte("changed"))
				case "profile-digest":
					b.RuntimeProfileDigest = sandbox.Digest([]byte("changed"))
				}
				s, e = session.New(caller.TenantID, r.SessionID, b, now)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.Transition(session.Ready, 0, now); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = a.evaluate(caller, r, s, r.SessionID, now); e != authority.ErrDenied {
				t.Fatal(e)
			}
		})
	}
}
