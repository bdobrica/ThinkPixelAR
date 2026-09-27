package postgres

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func (r *sessionRepository) Activate(ctx context.Context, s *session.Session, expected uint64, executionID primitives.ID) error {
	if s == nil || s.TenantID() != r.tenantID || s.State() != session.Active || s.StateVersion() != expected+1 {
		return errors.New("invalid Session admission")
	}
	result, err := r.tx.ExecContext(ctx, `UPDATE sessions SET state='ACTIVE',state_version=$3,execution_generation=$4,current_execution_id=$5,updated_at=$6 WHERE tenant_id=$1 AND session_id=$2 AND state_version=$7 AND state IN ('READY','IDLE') AND current_execution_id IS NULL AND execution_generation=$4-1`, r.tenantID, s.ID(), s.StateVersion(), s.ExecutionGeneration(), executionID, s.UpdatedAt(), expected)
	return affected("activate Session", result, err)
}
func (r *executionRepository) AddInput(ctx context.Context, id primitives.ID, input, digest string) error {
	if !utf8.ValidString(input) || len(input) == 0 || len(input) > 262144 || sandbox.Digest([]byte(input)) != digest {
		return errors.New("invalid Execution input")
	}
	_, err := r.tx.ExecContext(ctx, `INSERT INTO execution_inputs(tenant_id,execution_id,input,input_digest) VALUES($1,$2,$3,$4)`, r.tenantID, id, []byte(input), digest)
	return wrap("insert Execution input", err)
}
func (r *executionRepository) GetInput(ctx context.Context, id primitives.ID) (string, string, error) {
	var input []byte
	var digest string
	err := r.tx.QueryRowContext(ctx, `SELECT input,input_digest FROM execution_inputs WHERE tenant_id=$1 AND execution_id=$2`, r.tenantID, id).Scan(&input, &digest)
	if err != nil {
		return "", "", wrap("read Execution input", err)
	}
	if sandbox.Digest(input) != digest {
		return "", "", errors.New("Execution input integrity mismatch")
	}
	return string(input), digest, nil
}
func (r *eventRepository) NextSequence(ctx context.Context, id primitives.ID) (uint64, error) {
	_, err := r.tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.tenantID, id)
	if err != nil {
		return 0, wrap("initialize event stream", err)
	}
	var sequence uint64
	err = r.tx.QueryRowContext(ctx, `SELECT last_sequence+1 FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.tenantID, id).Scan(&sequence)
	return sequence, wrap("read event sequence", err)
}

// GetForUpdate serializes admission decisions before local authority evaluation.
func (r *sessionRepository) GetForUpdate(ctx context.Context, id primitives.ID) (*session.Session, error) {
	var found primitives.ID
	if err := r.tx.QueryRowContext(ctx, `SELECT session_id FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.tenantID, id).Scan(&found); err != nil {
		return nil, wrap("lock Session", err)
	}
	return r.Get(ctx, id)
}
