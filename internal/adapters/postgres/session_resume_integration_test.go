package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	checkpoint "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func resumeAccess(context.Context, app.ResumeRequest) error { return nil }
func resumePolicy(_ context.Context, i app.ResumeIntent) error {
	if i.Runtime.AuthorityMode != "LOCAL" || i.Runtime.RuntimeSpecDigest == "" || len(i.Manifest) == 0 || i.SandboxID == "" {
		return workspace.ErrIntegrity
	}
	return nil
}
func resumeReady(_ context.Context, i app.ResumeIntent, o app.ResumeObservation) error {
	if o.SandboxReference != "fixture/new/"+string(i.SandboxID) || o.AttachmentReference != string(i.AttachmentID) || o.EvidenceDigest != testDigest('d') {
		return workspace.ErrIntegrity
	}
	return nil
}
func resumeFixture(t *testing.T, retained bool) (*sql.DB, app.ResumeRequest, *checkpoint.RestoreValidator, primitives.ID) {
	t.Helper()
	db, r, v, old := suspendFixture(t, retained)
	susp, _ := postgres.NewSessionSuspends(db, suspendAllow, suspendVerify, v)
	got, err := susp.Suspend(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	rr := app.ResumeRequest(r)
	rr.ExpectedVersion = got.Version
	rr.OperationID = concurrencyIDs(t, time.Now(), 1)[0]
	return db, rr, v, old
}
func resumeStore(t *testing.T, db *sql.DB, v *checkpoint.RestoreValidator) *postgres.SessionResumes {
	t.Helper()
	s, err := postgres.NewSessionResumes(db, resumeAccess, resumePolicy, resumeReady, v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// This is a provider fixture, not evidence of CSI/harness/credential restoration.
// It models creation-before-response-loss and exact idempotent resource discovery.
type resumeProvider struct {
	mu                       sync.Mutex
	candidates               map[primitives.ID]string
	lose                     bool
	cleanupFails             bool
	calls, creates, cleanups int
}

func (p *resumeProvider) Reconcile(_ context.Context, i app.ResumeIntent) (app.ResumeObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.candidates == nil {
		p.candidates = map[primitives.ID]string{}
	}
	digest := i.Digest()
	if prior, ok := p.candidates[i.SandboxID]; ok && prior != digest {
		return app.ResumeObservation{}, workspace.ErrIntegrity
	} else if !ok {
		p.creates++
		p.candidates[i.SandboxID] = digest
	}
	if p.lose {
		p.lose = false
		return app.ResumeObservation{}, errors.New("ambiguous provider timeout")
	}
	return app.ResumeObservation{SandboxReference: "fixture/new/" + string(i.SandboxID), AttachmentReference: string(i.AttachmentID), EvidenceDigest: testDigest('d')}, nil
}
func (p *resumeProvider) Cleanup(_ context.Context, i app.ResumeIntent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleanups++
	if p.cleanupFails {
		return errors.New("provider unavailable")
	}
	delete(p.candidates, i.SandboxID)
	return nil
}
func TestSessionResumeReplacementReplay(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "idle"}[retained], func(t *testing.T) {
			ctx := context.Background()
			db, r, v, old := resumeFixture(t, retained)
			s := resumeStore(t, db, v)
			if retained {
				if _, _, err := s.Prepare(ctx, r); !errors.Is(err, workspace.ErrConflict) {
					t.Fatal("old compute still present", err)
				}
				bindings, _ := postgres.NewSandboxBindings(db)
				intent, err := bindings.LoadCompute(ctx, r.Caller.TenantID, old)
				if err != nil {
					t.Fatal(err)
				}
				if err = bindings.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
					t.Fatal(err)
				}
			}
			p := &resumeProvider{lose: true}
			worker, _ := app.NewResumer(s, p)
			pending, err := worker.Resume(ctx, r)
			if !errors.Is(err, workspace.ErrUnavailable) || pending.State != "RESUMING" {
				t.Fatal(pending, err)
			}
			if pending.SandboxID == old || pending.Generation != map[bool]int64{false: 0, true: 1}[retained] {
				t.Fatal("reused identity or advanced Execution epoch", pending)
			}
			// Admission is illegal during the durable operation, even after worker loss.
			store, _ := postgres.NewStore(db)
			err = store.WithinTransaction(ctx, r.Caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
				sess, e := repos.Sessions().GetForUpdate(ctx, r.SessionID)
				if e != nil {
					return e
				}
				return sess.Transition(domain.Active, sess.StateVersion(), time.Now())
			})
			if !errors.Is(err, domain.ErrIllegalTransition) {
				t.Fatal("admitted while resuming", err)
			}
			restarted := resumeStore(t, db, v)
			worker, _ = app.NewResumer(restarted, p)
			for _, err := range concurrently(5, func(int) error {
				result, e := worker.Resume(ctx, r)
				if e == nil && (result.SandboxID != pending.SandboxID || result.State != map[bool]string{false: "READY", true: "IDLE"}[retained] || result.Version != r.ExpectedVersion+2) {
					return errors.New("wrong replay")
				}
				return e
			}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if p.creates != 1 {
				t.Fatal("duplicate physical candidates", p.creates)
			}
			var count int
			for _, q := range []string{`SELECT count(*) FROM session_resume_operations WHERE tenant_id=$1`, `SELECT count(*) FROM workspaces WHERE tenant_id=$1 AND state='ATTACHED' AND current_attachment_id IS NOT NULL`} {
				if err = db.QueryRow(q, r.Caller.TenantID).Scan(&count); err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			if err = db.QueryRow(`SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND topic='session.resume'`, r.Caller.TenantID).Scan(&count); err != nil || count != 2 {
				t.Fatal("event duplication", count, err)
			}
			// No synthetic Execution/grant is minted by infrastructure reconstruction.
			if err = db.QueryRow(`SELECT count(*) FROM executions WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&count); err != nil || count != map[bool]int{false: 0, true: 1}[retained] {
				t.Fatal("resume created execution", count, err)
			}
			changed := r
			changed.ExpectedVersion++
			if _, _, err = s.Prepare(ctx, changed); !errors.Is(err, workspace.ErrConflict) {
				t.Fatal("changed digest", err)
			}
			denied, _ := postgres.NewSessionResumes(db, func(context.Context, app.ResumeRequest) error { return errors.New("private denial") }, resumePolicy, resumeReady, v)
			if _, _, err = denied.Prepare(ctx, r); !errors.Is(err, workspace.ErrUnavailable) {
				t.Fatal("historical disclosure", err)
			}
			if _, err = db.Exec(`UPDATE sessions SET state='CLOSING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
				t.Fatal(err)
			}
			calls := p.calls
			if result, err := worker.Resume(ctx, r); err != nil || result.SandboxID != pending.SandboxID || p.calls != calls {
				t.Fatal("historical replay recreated", result, err)
			}
		})
	}
}
func TestSessionResumeFailureCleanupAndClose(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(map[bool]string{false: "readiness-failure", true: "concurrent-close"}[closing], func(t *testing.T) {
			ctx := context.Background()
			db, r, v, _ := resumeFixture(t, false)
			s, _ := postgres.NewSessionResumes(db, resumeAccess, resumePolicy, func(context.Context, app.ResumeIntent, app.ResumeObservation) error {
				return errors.New("private readiness failure")
			}, v)
			i, _, err := s.Prepare(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			p := &resumeProvider{cleanupFails: true}
			o, err := p.Reconcile(ctx, i)
			if err != nil {
				t.Fatal(err)
			}
			if closing {
				if _, err = db.Exec(`UPDATE sessions SET state='CLOSING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Complete(ctx, r, o); err == nil {
				t.Fatal("published stale/unverified candidate")
			}
			failed, err := s.Fail(ctx, r)
			if err != nil || failed.State != "DEGRADED" {
				t.Fatal(failed, err)
			}
			var state string
			if err = db.QueryRow(`SELECT state FROM sessions WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&state); err != nil || state != map[bool]string{false: "DEGRADED", true: "CLOSING"}[closing] {
				t.Fatal(state, err)
			}
			worker, _ := app.NewResumer(s, p)
			if err = worker.Cleanup(ctx, r.Caller.TenantID, r.OperationID); !errors.Is(err, workspace.ErrUnavailable) {
				t.Fatal(err)
			}
			if _, done, err := s.CleanupIntent(ctx, r.Caller.TenantID, r.OperationID); err != nil || done {
				t.Fatal("lost cleanup", done, err)
			}
			p.cleanupFails = false
			if err = worker.Cleanup(ctx, r.Caller.TenantID, r.OperationID); err != nil {
				t.Fatal(err)
			}
			if err = worker.Cleanup(ctx, r.Caller.TenantID, r.OperationID); err != nil || p.cleanups != 2 {
				t.Fatal("cleanup replay", err, p.cleanups)
			}
			if _, err = s.Complete(ctx, r, o); err != nil {
				t.Fatal("historical failed replay", err)
			}
			var attached bool
			if err = db.QueryRow(`SELECT current_attachment_id IS NOT NULL FROM workspaces WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&attached); err != nil || attached {
				t.Fatal("failed writer retained after confirmed absence", attached, err)
			}
		})
	}
}
func TestSessionResumeReservationAndGuards(t *testing.T) {
	for _, name := range []string{"version", "tenant", "checkpoint", "policy", "corruption", "competing-operation", "stale-attachment", "readiness"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, r, v, _ := resumeFixture(t, false)
			s := resumeStore(t, db, v)
			switch name {
			case "version":
				r.ExpectedVersion++
			case "tenant":
				r.Caller.TenantID = concurrencyIDs(t, time.Now(), 1)[0]
			case "checkpoint":
				r.CheckpointID = concurrencyIDs(t, time.Now(), 1)[0]
			case "policy":
				s, _ = postgres.NewSessionResumes(db, resumeAccess, func(context.Context, app.ResumeIntent) error { return errors.New("revoked") }, resumeReady, v)
			case "corruption":
				if _, err := db.Exec(`UPDATE checkpoints SET state='DELETING',state_version=state_version+1 WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
					t.Fatal(err)
				}
			case "competing-operation":
				if _, _, err := s.Prepare(ctx, r); err != nil {
					t.Fatal(err)
				}
				r.OperationID = concurrencyIDs(t, time.Now(), 1)[0]
				r.ExpectedVersion++
			case "stale-attachment", "readiness":
				i, _, err := s.Prepare(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				if name == "stale-attachment" {
					if _, err = db.Exec(`UPDATE workspaces SET current_attachment_id=$2 WHERE tenant_id=$1`, r.Caller.TenantID, concurrencyIDs(t, time.Now(), 1)[0]); err != nil {
						t.Fatal(err)
					}
				}
				o := app.ResumeObservation{SandboxReference: "forged", AttachmentReference: string(i.AttachmentID), EvidenceDigest: testDigest('d')}
				if _, err = s.Complete(ctx, r, o); err == nil {
					t.Fatal("invalid completion accepted")
				}
				return
			}
			if _, _, err := s.Prepare(ctx, r); err == nil {
				t.Fatal("invalid preparation accepted")
			}
		})
	}
}

func TestSessionResumeAtomicOutbox(t *testing.T) {
	ctx := context.Background()
	db, r, v, _ := resumeFixture(t, false)
	s := resumeStore(t, db, v)
	// Reject the last transactional write without affecting other tenants/topics.
	reject := `ALTER TABLE outbox_messages ADD CONSTRAINT ses005_reject CHECK (aggregate_id <> '` + string(r.SessionID) + `'::uuid OR topic <> 'session.resume') NOT VALID`
	drop := `ALTER TABLE outbox_messages DROP CONSTRAINT ses005_reject`
	if _, err := db.Exec(reject); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`ALTER TABLE outbox_messages DROP CONSTRAINT IF EXISTS ses005_reject`) })
	if _, _, err := s.Prepare(ctx, r); err == nil {
		t.Fatal("outbox rejection ignored")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM session_resume_operations WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial reservation", count, err)
	}
	var version int64
	var attached bool
	if err := db.QueryRow(`SELECT s.state_version,w.current_attachment_id IS NOT NULL FROM sessions s JOIN workspaces w USING(tenant_id,session_id) WHERE s.tenant_id=$1`, r.Caller.TenantID).Scan(&version, &attached); err != nil || version != r.ExpectedVersion || attached {
		t.Fatal("partial fence", version, attached, err)
	}
	if _, err := db.Exec(drop); err != nil {
		t.Fatal(err)
	}
	i, _, err := s.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p := &resumeProvider{}
	o, _ := p.Reconcile(ctx, i)
	if _, err = db.Exec(reject); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Complete(ctx, r, o); err == nil {
		t.Fatal("partial publication accepted")
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM session_resume_operations WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&state); err != nil || state != "RESUMING" {
		t.Fatal(state, err)
	}
	if _, err = db.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Complete(ctx, r, o); err != nil || got.State != "READY" {
		t.Fatal(got, err)
	}
}

func TestSessionResumeWorkerRejectsRevokedCheckpoint(t *testing.T) {
	ctx := context.Background()
	db, r, v, _ := resumeFixture(t, false)
	s := resumeStore(t, db, v)
	p := &resumeProvider{lose: true}
	worker, _ := app.NewResumer(s, p)
	if _, err := worker.Resume(ctx, r); !errors.Is(err, workspace.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE checkpoints SET state='DELETING',state_version=state_version+1 WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
		t.Fatal(err)
	}
	got, err := worker.Resume(ctx, r)
	if err != nil || got.State != "DEGRADED" || p.calls != 1 {
		t.Fatal("reused invalid checkpoint", got, err, p.calls)
	}
	if err = worker.Cleanup(ctx, r.Caller.TenantID, r.OperationID); err != nil {
		t.Fatal(err)
	}
}
