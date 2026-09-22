package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type AgentdDelivery struct{ bindings *SandboxBindings }

var _ bootstrap.Journal = (*AgentdDelivery)(nil)

func NewAgentdDelivery(db *sql.DB) (*AgentdDelivery, error) {
	b, err := NewSandboxBindings(db)
	if err != nil {
		return nil, bootstrap.ErrBootstrap
	}
	return &AgentdDelivery{b}, nil
}
func (d *AgentdDelivery) transaction(ctx context.Context, tenant primitives.ID, f func(*sql.Tx) error) error {
	if d.bindings.transaction(ctx, tenant, f) != nil {
		return bootstrap.ErrBootstrap
	}
	return nil
}
func (d *AgentdDelivery) SavePlan(ctx context.Context, r bootstrap.Reference) error {
	if r.UID != "" || r.Namespace == "" || r.Name != "agentd-"+string(r.Record.CredentialID) || len(r.BundleDigest) != 71 || !r.Record.Bootstrap {
		return bootstrap.ErrBootstrap
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 16384 {
		return bootstrap.ErrBootstrap
	}
	return d.transaction(ctx, r.Record.Identity.TenantID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO agentd_bootstrap_delivery(tenant_id,credential_id,reference,expires_at) SELECT tenant_id,credential_id,$3::jsonb,expires_at FROM agentd_credentials WHERE tenant_id=$1 AND credential_id=$2 AND bootstrap AND consumed_at IS NULL AND expires_at>clock_timestamp() AND certificate_digest=$4 AND proof_digest=$5 AND sandbox_binding_id=$6 AND issuer_digest=$7 AND not_before=$8 AND expires_at=$9 AND EXISTS(SELECT 1 FROM agentd_credential_state s WHERE s.tenant_id=agentd_credentials.tenant_id AND s.sandbox_binding_id=agentd_credentials.sandbox_binding_id AND s.attempt_id=$10 AND s.latest_digest=agentd_credentials.certificate_digest) ON CONFLICT(tenant_id,credential_id) DO NOTHING`, r.Record.Identity.TenantID, r.Record.CredentialID, string(raw), r.Record.CertificateDigest, r.Record.ProofDigest, r.Record.Identity.SandboxID, r.Record.IssuerDigest, r.Record.NotBefore, r.Record.ExpiresAt, r.Record.Identity.AttemptID)
		if oneDeliveryRow(result, err) != nil {
			return bootstrap.ErrBootstrap
		}
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT reference=$3::jsonb AND NOT cleanup_requested AND NOT cleaned FROM agentd_bootstrap_delivery WHERE tenant_id=$1 AND credential_id=$2`, r.Record.Identity.TenantID, r.Record.CredentialID, string(raw)).Scan(&same)
		if err != nil || !same {
			return bootstrap.ErrBootstrap
		}
		return nil
	})
}
func scanDelivery(row interface{ Scan(...any) error }) (bootstrap.DeliveryEntry, error) {
	var e bootstrap.DeliveryEntry
	var raw []byte
	var uid string
	if row.Scan(&raw, &uid, &e.CleanupRequested, &e.Cleaned) != nil || json.Unmarshal(raw, &e.Reference) != nil {
		return e, bootstrap.ErrBootstrap
	}
	e.Reference.UID = uid
	return e, nil
}
func (d *AgentdDelivery) LoadDelivery(ctx context.Context, tenant, id primitives.ID) (bootstrap.DeliveryEntry, error) {
	var e bootstrap.DeliveryEntry
	err := d.transaction(ctx, tenant, func(tx *sql.Tx) error {
		var err error
		e, err = scanDelivery(tx.QueryRowContext(ctx, `SELECT reference,provider_uid,cleanup_requested,cleaned FROM agentd_bootstrap_delivery WHERE tenant_id=$1 AND credential_id=$2`, tenant, id))
		return err
	})
	return e, err
}
func (d *AgentdDelivery) BindUID(ctx context.Context, r bootstrap.Reference) error {
	if r.UID == "" || len(r.UID) > 256 {
		return bootstrap.ErrBootstrap
	}
	uid := r.UID
	r.UID = ""
	raw, err := json.Marshal(r)
	if err != nil {
		return bootstrap.ErrBootstrap
	}
	return d.transaction(ctx, r.Record.Identity.TenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery SET provider_uid=$3 WHERE tenant_id=$1 AND credential_id=$2 AND reference=$4::jsonb AND (provider_uid='' OR provider_uid=$3) AND NOT cleaned`, r.Record.Identity.TenantID, r.Record.CredentialID, uid, string(raw))
		return oneDeliveryRow(res, err)
	})
}
func oneDeliveryRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return bootstrap.ErrBootstrap
	}
	return nil
}
func (d *AgentdDelivery) RequestCleanup(ctx context.Context, tenant, id primitives.ID) error {
	return d.transaction(ctx, tenant, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery SET cleanup_requested=true,retry_after=clock_timestamp()+interval '5 seconds' WHERE tenant_id=$1 AND credential_id=$2`, tenant, id)
		return oneDeliveryRow(res, err)
	})
}
func (d *AgentdDelivery) CompleteCleanup(ctx context.Context, r bootstrap.Reference) error {
	if r.UID == "" {
		return bootstrap.ErrBootstrap
	}
	uid := r.UID
	r.UID = ""
	raw, err := json.Marshal(r)
	if err != nil {
		return bootstrap.ErrBootstrap
	}
	return d.transaction(ctx, r.Record.Identity.TenantID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery SET cleaned=true WHERE tenant_id=$1 AND credential_id=$2 AND provider_uid=$3 AND reference=$4::jsonb AND cleanup_requested`, r.Record.Identity.TenantID, r.Record.CredentialID, uid, string(raw))
		return oneDeliveryRow(res, err)
	})
}
func (d *AgentdDelivery) DueCleanup(ctx context.Context, tenant primitives.ID, limit int) ([]bootstrap.DeliveryEntry, error) {
	if limit < 1 || limit > 128 {
		return nil, bootstrap.ErrBootstrap
	}
	entries := []bootstrap.DeliveryEntry{}
	err := d.transaction(ctx, tenant, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT reference,provider_uid,cleanup_requested,cleaned FROM agentd_bootstrap_delivery WHERE tenant_id=$1 AND NOT cleaned AND retry_after<=clock_timestamp() AND (cleanup_requested OR expires_at<=clock_timestamp()) ORDER BY retry_after,expires_at,credential_id LIMIT $2`, tenant, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	return entries, err
}
