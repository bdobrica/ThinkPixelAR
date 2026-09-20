package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	work "github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var _ sandbox.ComputeStore = (*SandboxBindings)(nil)
var observationCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// queueCompute shares the intent transaction. A collision with another kind of
// work is an integrity failure, never permission to silently lose reconciliation.
func queueCompute(ctx context.Context, tx *sql.Tx, tenant, id primitives.ID) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO reconciliation_work(tenant_id,work_id,work_kind,target_type,target_id,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox.reconcile','sandbox',$3,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT(tenant_id,work_id) DO NOTHING`, tenant, id, id)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT work_kind='sandbox.reconcile' AND target_type='sandbox' AND target_id=$3 FROM reconciliation_work WHERE tenant_id=$1 AND work_id=$2`, tenant, id, id).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return sandbox.ErrIntegrity
	}
	return nil
}

func (s *SandboxBindings) LoadCompute(ctx context.Context, tenant, id primitives.ID) (sandbox.ComputeIntent, error) {
	var intent sandbox.ComputeIntent
	err := s.transaction(ctx, tenant, func(tx *sql.Tx) error { var err error; intent, err = loadCompute(ctx, tx, tenant, id); return err })
	return intent, err
}
func loadCompute(ctx context.Context, tx *sql.Tx, tenant, id primitives.ID) (sandbox.ComputeIntent, error) {
	var intent sandbox.ComputeIntent
	b, _, err := readSandboxBinding(ctx, tx, tenant, id, false)
	if err != nil {
		return intent, err
	}
	// Match the existing lifecycle lock order: Session/Execution/Attempt, binding.
	current, err := lockSandboxFence(ctx, tx, b.Request)
	if err != nil {
		return intent, err
	}
	b, revision, err := readSandboxBinding(ctx, tx, tenant, id, true)
	if err != nil {
		return intent, err
	}
	intent = sandbox.ComputeIntent{Binding: b, Revision: revision, Current: current, Desired: sandbox.ComputeRunning, Operation: b.Request.Operation}
	if err = tx.QueryRowContext(ctx, `SELECT state_version FROM sandbox_bindings WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, id).Scan(&intent.Version); err != nil {
		return intent, err
	}

	if revision == 0 && b.ProviderReference != "" {
		var cleanup sandbox.Operation
		err = tx.QueryRowContext(ctx, `SELECT cleanup_operation_id,request_digest FROM cleanup_intents WHERE tenant_id=$1 AND owner_type='sandbox-binding' AND owner_id=$2 AND target_type='sandbox' AND provider_kind=$3 AND external_reference=$4 AND ownership_proof_digest=$5 AND state IN ('PENDING','CONFIRMED')`, tenant, id, b.Request.Profile.Implementation.ProviderKind, b.ProviderReference, b.Request.Operation.Digest).Scan(&cleanup.ID, &cleanup.Digest)
		if err == nil {
			if cleanup.Digest != sandbox.LifecycleDigest(tenant, id, "release", cleanup.ID) {
				return intent, sandbox.ErrIntegrity
			}
			intent.Desired, intent.Operation, intent.ReleaseAuthorized = sandbox.ComputeReleased, cleanup, true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return intent, err
		}
		err = nil
	}
	if revision > 0 {
		var kind string
		err = tx.QueryRowContext(ctx, `SELECT kind,operation_id,request_digest FROM sandbox_operations WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND revision=$3`, tenant, id, revision).Scan(&kind, &intent.Operation.ID, &intent.Operation.Digest)
		if err != nil {
			return intent, err
		}
		// Native suspension is not the Session's release-and-restore protocol.
		if kind != "release" {
			return intent, sandbox.ErrUnsupported
		}
		intent.Desired = sandbox.ComputeReleased
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cleanup_intents WHERE tenant_id=$1 AND owner_type='sandbox-binding' AND owner_id=$2 AND target_type='sandbox' AND provider_kind=$7 AND external_reference=$3 AND cleanup_operation_id=$4 AND request_digest=$5 AND ownership_proof_digest=$6 AND state IN ('PENDING','CONFIRMED'))`, tenant, id, b.ProviderReference, intent.Operation.ID, intent.Operation.Digest, b.Request.Operation.Digest, b.Request.Profile.Implementation.ProviderKind).Scan(&intent.ReleaseAuthorized)
	}
	return intent, err
}

func (s *SandboxBindings) RecordCompute(ctx context.Context, expected sandbox.ComputeIntent, observed sandbox.ComputeObservation) error {
	if !observationCode.MatchString(observed.Code) {
		return sandbox.ErrInvalid
	}
	switch observed.State {
	case sandbox.Unknown, sandbox.Provisioning, sandbox.Requested, sandbox.Resuming, sandbox.Ready, sandbox.Releasing, sandbox.Released:
	default:
		return sandbox.ErrInvalid
	}
	if observed.Converged != (observed.State == sandbox.Ready || observed.State == sandbox.Released) || observed.RecoveryRequired && observed.State != sandbox.Unknown {
		return sandbox.ErrInvalid
	}
	if observed.State == sandbox.Ready && !observed.Effective.Verified {
		return sandbox.ErrIntegrity
	}
	return s.transaction(ctx, expected.Binding.Request.Scope.TenantID, func(tx *sql.Tx) error {
		scope := expected.Binding.Request.Scope
		current, err := loadCompute(ctx, tx, scope.TenantID, scope.SandboxID)
		if err != nil {
			return err
		}
		if (current.Desired == sandbox.ComputeRunning && current.Current != expected.Current) || current.Version != expected.Version || current.Revision != expected.Revision || current.Operation != expected.Operation || current.Desired != expected.Desired || current.Binding.Request.Operation != expected.Binding.Request.Operation || current.Binding.ProviderReference != expected.Binding.ProviderReference {
			return sandbox.ErrConflict
		}
		if current.Desired == sandbox.ComputeRunning && !current.Current && !(observed.RecoveryRequired && observed.Code == "FENCED_COMPUTE" && !expected.Current) || current.Desired == sandbox.ComputeReleased && !current.ReleaseAuthorized {
			return sandbox.ErrConflict
		}
		if current.Desired == sandbox.ComputeRunning && (observed.State == sandbox.Released || observed.State == sandbox.Releasing) || current.Desired == sandbox.ComputeReleased && observed.State != sandbox.Released && observed.State != sandbox.Releasing && observed.State != sandbox.Unknown {
			return sandbox.ErrIntegrity
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		var facts any
		if observed.Effective.Verified {
			raw, err := json.Marshal(observed.Effective)
			if err != nil {
				return sandbox.ErrInvalid
			}
			facts = sandbox.Digest(raw)
		}
		_, err = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET state=$3,reason=$4,effective_facts_digest=$5,observed_at=$6,updated_at=$6,state_version=state_version+1 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID, observed.State, observed.Code, facts, now)
		if err != nil {
			return err
		}
		if observed.RecoveryRequired {
			return recordComputeRecovery(ctx, tx, current, observed, now)
		}
		if observed.State == sandbox.Released {
			_, err = tx.ExecContext(ctx, `UPDATE cleanup_intents SET state='CONFIRMED',state_version=state_version+1,attempts=attempts+1,last_error_code=NULL,confirmed_at=$3,updated_at=$3 WHERE tenant_id=$1 AND cleanup_operation_id=$2 AND owner_type='sandbox-binding' AND owner_id=$4 AND state='PENDING'`, scope.TenantID, current.Operation.ID, now, scope.SandboxID)
		}
		return err
	})
}

// ClaimCompute leases only compute work. Other application workers retain their
// own queues. Database time defines lease expiry; limits keep each claim bounded.
func (s *SandboxBindings) ClaimCompute(ctx context.Context, tenant, owner primitives.ID, lease time.Duration, limit int) ([]*work.Work, error) {
	if _, err := primitives.ParseID(string(owner)); err != nil || lease < time.Second || lease > 5*time.Minute || limit < 1 || limit > 32 {
		return nil, sandbox.ErrInvalid
	}
	var result []*work.Work
	err := s.transaction(ctx, tenant, func(tx *sql.Tx) error {
		var now time.Time
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `WITH candidates AS (SELECT tenant_id,work_id FROM reconciliation_work WHERE tenant_id=$1 AND work_kind='sandbox.reconcile' AND target_type='sandbox' AND next_attempt_at<=$2 AND (state='PENDING' OR (state='CLAIMED' AND claim_expires_at<=$2)) ORDER BY next_attempt_at,work_id LIMIT $3 FOR UPDATE SKIP LOCKED), claimed AS (UPDATE reconciliation_work w SET state='CLAIMED',attempts=w.attempts+1,claim_owner_id=$4,claim_fence=w.claim_fence+1,claim_expires_at=$5,updated_at=$2 FROM candidates c WHERE w.tenant_id=c.tenant_id AND w.work_id=c.work_id RETURNING w.*) SELECT work_id,work_kind,target_type,target_id,state,attempts,claim_owner_id,claim_fence,next_attempt_at,claim_expires_at,last_error_code,created_at,updated_at,completed_at FROM claimed ORDER BY next_attempt_at,work_id`, tenant, now, limit, owner, now.Add(lease))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanReconciliation(rows, tenant)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}

func (s *SandboxBindings) FinishCompute(ctx context.Context, claim *work.Work, complete bool, code string, retry time.Duration) error {
	if claim == nil || !observationCode.MatchString(code) || retry < time.Second || retry > time.Hour {
		return sandbox.ErrInvalid
	}
	return s.transaction(ctx, claim.TenantID(), func(tx *sql.Tx) error {
		saved, err := scanReconciliation(tx.QueryRowContext(ctx, reconciliationSelect+` WHERE tenant_id=$1 AND work_id=$2 FOR UPDATE`, claim.TenantID(), claim.ID()), claim.TenantID())
		if err != nil {
			return err
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		expiry, ok := saved.ClaimExpiresAt()
		if saved.Kind() != "sandbox.reconcile" || saved.TargetType() != "sandbox" || saved.TargetID() != claim.TargetID() || saved.OwnerID() != claim.OwnerID() || saved.ClaimFence() != claim.ClaimFence() || !ok || !expiry.After(now) {
			return sandbox.ErrConflict
		}
		if complete {
			err = saved.Complete(claim.OwnerID(), claim.ClaimFence(), now)
		} else {
			err = saved.Reschedule(claim.OwnerID(), claim.ClaimFence(), now.Add(retry), code, now)
		}
		if err != nil {
			return sandbox.ErrConflict
		}
		return (*reconciliationRepository)(&repositories{tx: tx, tenantID: claim.TenantID()}).Update(ctx, saved, claim.ClaimFence())
	})
}
