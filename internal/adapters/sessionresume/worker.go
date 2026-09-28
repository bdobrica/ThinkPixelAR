//go:build linux

package sessionresume

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

type Materializer struct {
	bundle *Bundle
	store  *postgres.SessionResumes
}

// Open composes current operator access, signed export validation, local grant
// retirement, durable lifecycle coordination and the concrete Kubernetes lane.
// It performs no migrations and cannot create Sessions or admit Executions.
func Open(db *sql.DB, path string) (*sessions.Resumer, *Bundle, error) {
	b, err := Load(path)
	if err != nil {
		return nil, nil, err
	}
	v, err := b.Validator()
	if err != nil {
		return nil, nil, err
	}
	m := &Materializer{bundle: b}
	policy := func(ctx context.Context, i sessions.ResumeIntent) error {
		if err := b.Policy(ctx, i); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return workspace.ErrUnavailable
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, i.Request.Caller.TenantID); err != nil {
			return workspace.ErrUnavailable
		}
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM local_authority_grants WHERE tenant_id=$1 AND session_id=$2 AND state='ACTIVE')`, i.Request.Caller.TenantID, i.Request.SessionID).Scan(&active)
		if err != nil || active {
			return workspace.ErrUnavailable
		}
		return nil
	}
	m.store, err = postgres.NewSessionResumes(db, b.Access, policy, m.Ready, v)
	if err != nil {
		return nil, nil, err
	}
	worker, err := sessions.NewResumer(m.store, m)
	return worker, b, err
}
func (b *Bundle) Request() sessions.ResumeRequest { return b.config.Request }

func (m *Materializer) Reconcile(ctx context.Context, i sessions.ResumeIntent) (sessions.ResumeObservation, error) {
	var result sessions.ResumeObservation
	err := m.store.WithResumeCandidate(ctx, i, false, func(ctx context.Context) error {
		b := m.bundle
		if err := b.allocate(ctx, i); err != nil {
			return err
		}
		c, err := b.inspect(ctx, i)
		if err != nil {
			return err
		}
		if err = b.hostProof(ctx, i, c); err != nil {
			return err
		}
		p, err := b.probe(ctx, i)
		if err != nil {
			return err
		}
		after, err := b.inspect(ctx, i)
		if err != nil {
			return err
		}
		if after != c {
			return workspace.ErrIntegrity
		}
		result = b.observation(i, c, p)
		return nil
	})
	return result, err
}

// Ready is called under the store's publication fence. It reads live ownership,
// effective Pod settings, exact restored bytes and an independent worker KVM
// proof anew; it does not trust a saved receipt or the prior materializer result.
func (m *Materializer) Ready(ctx context.Context, i sessions.ResumeIntent, o sessions.ResumeObservation) error {
	b := m.bundle
	c, err := b.inspect(ctx, i)
	if err != nil {
		return err
	}
	if err = b.hostProof(ctx, i, c); err != nil {
		return err
	}
	p, err := b.probe(ctx, i)
	if err != nil {
		return err
	}
	after, err := b.inspect(ctx, i)
	if err != nil {
		return err
	}
	if after != c || o != b.observation(i, c, p) {
		return workspace.ErrIntegrity
	}
	return nil
}

// Cleanup never deletes export files or PVCs. Kubernetes UID preconditions and
// foreground deletion protect replacements. Absence of both Sandbox and Pod is
// required; an outage, terminating Pod or ambiguous response remains pending.
func (m *Materializer) Cleanup(ctx context.Context, i sessions.ResumeIntent) error {
	return m.store.WithResumeCandidate(ctx, i, true, func(ctx context.Context) error {
		b := m.bundle
		if err := b.namespace(ctx); err != nil {
			return err
		}
		name := candidateName(i)
		sb, err := b.get(ctx, "sandbox", name)
		if err != nil {
			return err
		}
		if sb != nil {
			if !owned(sb, i) {
				return workspace.ErrIntegrity
			}
			if metadata(sb)["deletionTimestamp"] == nil {
				raw, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": uid(sb)}, "propagationPolicy": "Foreground"})
				if _, err = b.command(ctx, raw, "delete", "--raw", "/apis/agents.x-k8s.io/v1beta1/namespaces/"+b.config.Namespace+"/sandboxes/"+name, "-f", "-"); err != nil {
					return err
				}
			}
		}
		for _, kind := range []string{"sandbox", "pod"} {
			v, err := b.get(ctx, kind, name)
			if err != nil {
				return err
			}
			if v != nil {
				return workspace.ErrUnavailable
			}
		}
		return nil
	})
}

// Run is a restartable, bounded operator worker. Re-running the identical config
// resumes the durable operation; historical replay never touches the provider.
func Run(ctx context.Context, db *sql.DB, path string, cleanup bool) (sessions.ResumeResult, error) {
	worker, b, err := Open(db, path)
	if err != nil {
		return sessions.ResumeResult{}, err
	}
	request := b.Request()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for {
		var result sessions.ResumeResult
		if cleanup {
			err = worker.Cleanup(ctx, request.Caller.TenantID, request.OperationID)
		} else {
			result, err = worker.Resume(ctx, request)
		}
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, workspace.ErrUnavailable) {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, workspace.ErrUnavailable
		case <-time.After(time.Second):
		}
	}
}
