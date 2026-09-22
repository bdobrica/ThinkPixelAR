package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// RecoverExpiredAgentd atomically fences compute whose latest identity can no
// longer reconnect. The immutable bootstrap projection cannot recover in place.
// Absence of a stream or expiry of an older certificate is not sufficient.
// Replacement remains pending durable recovery work, never command replay.
func (s *SandboxBindings) RecoverExpiredAgentd(ctx context.Context, tenant, id primitives.ID) error {
	return s.transaction(ctx, tenant, func(tx *sql.Tx) error {
		intent, err := loadCompute(ctx, tx, tenant, id)
		if err != nil || intent.Desired == sandbox.ComputeReleased {
			return err
		}
		var expired bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM agentd_credential_state s JOIN agentd_credentials c
			ON c.tenant_id=s.tenant_id AND c.sandbox_binding_id=s.sandbox_binding_id AND c.certificate_digest=s.latest_digest
			WHERE s.tenant_id=$1 AND s.sandbox_binding_id=$2 AND s.attempt_id=$3
			AND c.expires_at<=clock_timestamp()
			AND (s.connection_deadline IS NULL OR s.connection_deadline<=clock_timestamp()))`, tenant, id, intent.Binding.Request.Scope.AttemptID).Scan(&expired)
		if err != nil || !expired {
			return err
		}
		// loadCompute holds the same aggregate/binding locks used by issuance,
		// reconnect and frame admission; renewal cannot race this decision.
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		observation := sandbox.ComputeObservation{State: sandbox.Unknown, Code: "AGENTD_CREDENTIALS_EXPIRED", RecoveryRequired: true}
		_, err = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET state='UNKNOWN',reason=$3,effective_facts_digest=NULL,observed_at=$4,updated_at=$4,state_version=state_version+1 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, id, observation.Code, now)
		if err != nil {
			return err
		}
		return recordComputeRecovery(ctx, tx, intent, observation, now)
	})
}
