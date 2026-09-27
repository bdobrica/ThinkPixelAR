package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func (r *executionRepository) GetForUpdate(ctx context.Context, id primitives.ID) (*execution.Execution, error) {
	var found primitives.ID
	if err := r.tx.QueryRowContext(ctx, `SELECT execution_id FROM executions WHERE tenant_id=$1 AND execution_id=$2 FOR UPDATE`, r.tenantID, id).Scan(&found); err != nil {
		return nil, wrap("lock Execution", err)
	}
	return r.Get(ctx, id)
}
func (r *executionRepository) AddSignal(ctx context.Context, v persistence.SignalRecord) error {
	if len(v.Payload) == 0 || len(v.Payload) > 65536 || !json.Valid(v.Payload) || sandbox.Digest(v.Payload) != v.Digest {
		return errors.New("invalid signal payload")
	}
	_, err := r.tx.ExecContext(ctx, `INSERT INTO execution_signals(tenant_id,signal_id,execution_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5)`, r.tenantID, v.ID, v.ExecutionID, v.Payload, v.Digest)
	return wrap("insert signal", err)
}
func (r *executionRepository) GetSignal(ctx context.Context, id primitives.ID) (persistence.SignalRecord, error) {
	v := persistence.SignalRecord{ID: id}
	err := r.tx.QueryRowContext(ctx, `SELECT execution_id,payload,payload_digest FROM execution_signals WHERE tenant_id=$1 AND signal_id=$2`, r.tenantID, id).Scan(&v.ExecutionID, &v.Payload, &v.Digest)
	if err != nil {
		return persistence.SignalRecord{}, wrap("read signal", err)
	}
	if sandbox.Digest(v.Payload) != v.Digest {
		return persistence.SignalRecord{}, errors.New("signal integrity mismatch")
	}
	return v, nil
}
