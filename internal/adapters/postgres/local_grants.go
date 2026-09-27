package postgres

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func (r *repositories) LocalGrants() persistence.LocalGrantRepository {
	return (*localGrantRepository)(r)
}

type localGrantRepository repositories

func (r *localGrantRepository) Add(ctx context.Context, v persistence.LocalGrantRecord) error {
	_, err := r.tx.ExecContext(ctx, `INSERT INTO local_authority_grants(tenant_id,grant_id,session_id,snapshot,snapshot_digest) VALUES($1,$2,$3,$4,$5)`, r.tenantID, v.ID, v.SessionID, v.Snapshot, v.Digest)
	return wrap("insert local grant", err)
}
func (r *localGrantRepository) Get(ctx context.Context, id primitives.ID) (persistence.LocalGrantRecord, error) {
	var v persistence.LocalGrantRecord
	err := r.tx.QueryRowContext(ctx, `SELECT grant_id,session_id,snapshot,snapshot_digest,state,state_version FROM local_authority_grants WHERE tenant_id=$1 AND grant_id=$2 FOR UPDATE`, r.tenantID, id).Scan(&v.ID, &v.SessionID, &v.Snapshot, &v.Digest, &v.State, &v.Version)
	return v, wrap("read local grant", err)
}
func (r *localGrantRepository) Transition(ctx context.Context, id primitives.ID, version uint64, state string, at time.Time) error {
	result, err := r.tx.ExecContext(ctx, `UPDATE local_authority_grants SET state=$3,state_version=state_version+1,terminal_at=$4 WHERE tenant_id=$1 AND grant_id=$2 AND state_version=$5 AND state='ACTIVE'`, r.tenantID, id, state, at, version)
	return affected("transition local grant", result, err)
}
