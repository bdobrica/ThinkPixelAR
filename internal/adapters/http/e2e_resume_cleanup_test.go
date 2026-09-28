//go:build linux

package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sessionresume"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

// Explicitly selected operator fixture: abandon only a known pending resume and
// exercise the concrete cleanup worker. No permissive forward-work adapter is
// supplied and no completed Session is failed or deleted.
func TestStandaloneKubernetesAbandonedResumeCleanup(t *testing.T) {
	path := os.Getenv("THINKPIXELAR_E2E_CLEANUP_CONFIG")
	if path == "" {
		t.Skip("select a protected disposable pending-resume config")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	raw, err := sessionresume.ReadFile(path, 1<<20)
	e2eCheck(t, err)
	var cfg sessionresume.Config
	e2eCheck(t, json.Unmarshal(raw, &cfg))
	bundle, err := sessionresume.Load(path)
	e2eCheck(t, err)
	validator, err := bundle.Validator()
	e2eCheck(t, err)
	db, err := sql.Open("pgx", os.Getenv("THINKPIXELAR_TEST_DATABASE_URL"))
	e2eCheck(t, err)
	defer db.Close()
	store, err := postgres.NewSessionResumes(db, func(context.Context, sessions.ResumeRequest) error { return workspace.ErrUnavailable }, func(context.Context, sessions.ResumeIntent) error { return workspace.ErrUnavailable }, func(context.Context, sessions.ResumeIntent, sessions.ResumeObservation) error {
		return workspace.ErrUnavailable
	}, validator)
	e2eCheck(t, err)
	result, err := store.Fail(ctx, cfg.Request)
	e2eCheck(t, err)
	if result.State != "DEGRADED" {
		t.Fatal("cleanup fixture requires a pending/abandoned operation")
	}
	k := &e2eKube{t: t, ctx: ctx, host: cfg.SSH, namespace: cfg.Namespace}
	name := "resume-" + string(result.SandboxID)
	workspaceUID := e2eUID(k.object("pvc", name+"-workspace"))
	stateUID := e2eUID(k.object("pvc", name+"-state"))
	before := map[string]string{}
	for _, name := range []string{"workspace", "restore", "rollout"} {
		b, e := os.ReadFile(filepath.Join(cfg.Directory, name))
		e2eCheck(t, e)
		before[name] = string(b)
	}
	_, err = sessionresume.Run(ctx, db, path, true)
	e2eCheck(t, err)
	// A repeated cleanup must be an idempotent no-op.
	_, err = sessionresume.Run(ctx, db, path, true)
	e2eCheck(t, err)
	for _, kind := range []string{"sandbox", "pod"} {
		if len(k.must(nil, "-n", cfg.Namespace, "get", kind, name, "--ignore-not-found", "-o", "name")) != 0 {
			t.Fatal("candidate still present")
		}
	}
	if e2eUID(k.object("pvc", name+"-workspace")) != workspaceUID || e2eUID(k.object("pvc", name+"-state")) != stateUID {
		t.Fatal("cleanup changed durable PVCs")
	}
	for name, expected := range before {
		b, e := os.ReadFile(filepath.Join(cfg.Directory, name))
		e2eCheck(t, e)
		if string(b) != expected {
			t.Fatal("cleanup changed export")
		}
	}
	var cleaned bool
	e2eCheck(t, db.QueryRowContext(ctx, `SELECT cleaned FROM session_resume_operations WHERE tenant_id=$1 AND operation_id=$2`, cfg.Request.Caller.TenantID, cfg.Request.OperationID).Scan(&cleaned))
	if !cleaned {
		t.Fatal("physical absence not published")
	}
	t.Log("SES-005 live abandoned-candidate cleanup PASS; replay, compute absence, PVC/export preservation")
}
