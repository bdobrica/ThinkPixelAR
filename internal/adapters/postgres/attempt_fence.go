package postgres

import (
	"context"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Lock parents before the Attempt row, matching lifecycle/recovery lock order.
// The database trigger independently rechecks the fence under these locks.
func (r *attemptRepository) lockParents(ctx context.Context, executionID primitives.ID) error {
	var id primitives.ID
	err := r.tx.QueryRowContext(ctx, `SELECT s.session_id FROM sessions s
		WHERE s.tenant_id=$1 AND s.session_id=(SELECT e.session_id FROM executions e WHERE e.tenant_id=$1 AND e.execution_id=$2)
		FOR UPDATE`, r.tenantID, executionID).Scan(&id)
	if err != nil {
		return wrap("lock Attempt Session", err)
	}
	err = r.tx.QueryRowContext(ctx, `SELECT execution_id FROM executions WHERE tenant_id=$1 AND execution_id=$2 FOR UPDATE`, r.tenantID, executionID).Scan(&id)
	return wrap("lock Attempt Execution", err)
}
