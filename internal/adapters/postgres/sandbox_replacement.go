package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// SandboxReplacements implements the pre-execution infrastructure-loss lane.
// Running work and any dispatched command require checkpoint/outcome recovery;
// this implementation deliberately refuses to replay them.
type SandboxReplacements struct {
	bindings *SandboxBindings
	policy   sandbox.ReplacementPolicy
}

func NewSandboxReplacements(db *sql.DB, policy sandbox.ReplacementPolicy) (*SandboxReplacements, error) {
	if db == nil || policy == nil {
		return nil, sandbox.ErrInvalid
	}
	bindings, _ := NewSandboxBindings(db)
	return &SandboxReplacements{bindings, policy}, nil
}

func (s *SandboxReplacements) ReplaceDeleted(ctx context.Context, tenant, lost primitives.ID, resources sandbox.ReplacementResources) (sandbox.Binding, error) {
	var result sandbox.Binding
	if _, err := primitives.ParseID(string(lost)); err != nil {
		return result, sandbox.ErrInvalid
	}
	for _, id := range []primitives.ID{resources.AttemptID, resources.SandboxID, resources.AcquireOperationID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return result, sandbox.ErrInvalid
		}
	}
	for _, ref := range []string{resources.AttachmentReference, resources.BootstrapReference} {
		if _, err := primitives.BoundedString(ref, 1, 2048, 2048); err != nil {
			return result, sandbox.ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := s.bindings.transaction(ctx, tenant, func(tx *sql.Tx) error {
		if err := lockSandboxOperation(ctx, tx, tenant, string(resources.AcquireOperationID)); err != nil {
			return err
		}
		old, _, err := readSandboxBinding(ctx, tx, tenant, lost, false)
		if err != nil {
			return err
		}
		scope := old.Request.Scope
		// Shared Session/Execution/Attempt lock order serializes loss, cancellation,
		// admission, transport mutation and competing recovery decisions.
		if _, err = lockSandboxFence(ctx, tx, old.Request); err != nil {
			return err
		}
		old, _, err = readSandboxBinding(ctx, tx, tenant, lost, true)
		if err != nil {
			return err
		}
		var replacement primitives.ID
		err = tx.QueryRowContext(ctx, `SELECT new_sandbox_id FROM sandbox_replacements WHERE tenant_id=$1 AND old_sandbox_id=$2`, tenant, lost).Scan(&replacement)
		if err == nil {
			saved, _, e := readSandboxBinding(ctx, tx, tenant, replacement, true)
			if e != nil {
				return e
			}
			if saved.Request.Scope.AttemptID != resources.AttemptID || saved.Request.Scope.SandboxID != resources.SandboxID || saved.Request.Operation.ID != string(resources.AcquireOperationID) || saved.Request.Workspace.Reference != resources.AttachmentReference || saved.Request.BootstrapReference != resources.BootstrapReference {
				return sandbox.ErrConflict
			}
			current, e := lockSandboxFence(ctx, tx, saved.Request)
			if e != nil {
				return e
			}
			var live bool
			e = tx.QueryRowContext(ctx, `SELECT release_operation_id IS NULL AND state NOT IN ('RELEASED','RELEASING','FAILED') FROM sandbox_bindings WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, replacement).Scan(&live)
			if e != nil {
				return e
			}
			if !current || !live {
				return sandbox.ErrConflict
			}
			if e = s.policy.CheckReplacement(ctx, old, saved.Request); e != nil {
				return sandbox.ErrPermission
			}
			if e = replacementDeadline(ctx, tx, saved.Request); e != nil {
				return e
			}
			result = saved
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var safe bool
		var attemptVersion, sessionVersion uint64
		err = tx.QueryRowContext(ctx, `SELECT
   s.state='DEGRADED' AND s.recovery_state='ACTIVE' AND s.current_execution_id=e.execution_id
   AND s.execution_generation=$5 AND e.session_generation=$5 AND e.state='MATERIALIZING'
   AND e.deadline>clock_timestamp() AND e.deadline >= $7 AND $7::timestamptz>clock_timestamp()
   AND a.is_current AND a.execution_generation=$5 AND a.attempt_no=$6
   AND a.state IN ('PENDING','ACQUIRING','STARTING') AND a.harness_binding_reference IS NULL
   AND b.state='RELEASED' AND b.reason='COMPUTE_ABSENT' AND b.provider_reference IS NOT NULL
   AND EXISTS(SELECT 1 FROM cleanup_intents c WHERE c.tenant_id=b.tenant_id AND c.owner_type='sandbox-binding' AND c.owner_id=b.sandbox_binding_id AND c.target_type='sandbox' AND c.provider_kind=b.provider_kind AND c.external_reference=b.provider_reference AND c.cleanup_operation_id=b.release_operation_id AND c.request_digest=b.release_request_digest AND c.ownership_proof_digest=b.acquire_request_digest AND c.state='CONFIRMED')
   AND EXISTS(SELECT 1 FROM reconciliation_work w WHERE w.tenant_id=b.tenant_id AND w.work_kind='sandbox.recover' AND w.target_type='sandbox' AND w.target_id=b.sandbox_binding_id AND w.last_error_code='BOUND_COMPUTE_MISSING')
   AND NOT EXISTS(SELECT 1 FROM harness_bindings h WHERE h.tenant_id=e.tenant_id AND h.execution_id=e.execution_id)
   AND NOT EXISTS(SELECT 1 FROM agentd_commands c JOIN sandbox_bindings cb ON cb.tenant_id=c.tenant_id AND cb.sandbox_binding_id=c.sandbox_binding_id WHERE cb.tenant_id=e.tenant_id AND cb.execution_id=e.execution_id)
   AND NOT EXISTS(SELECT 1 FROM agentd_credential_state c WHERE c.tenant_id=b.tenant_id AND c.sandbox_binding_id=b.sandbox_binding_id AND c.connection_id IS NOT NULL)
   AND NOT EXISTS(SELECT 1 FROM agentd_bootstrap_delivery d JOIN agentd_credentials c USING(tenant_id,credential_id) WHERE c.tenant_id=b.tenant_id AND c.sandbox_binding_id=b.sandbox_binding_id AND NOT d.cleaned AND GREATEST(d.expires_at,c.expires_at)>clock_timestamp()),
   a.state_version,s.state_version
   FROM sessions s JOIN executions e ON e.tenant_id=s.tenant_id AND e.session_id=s.session_id
   JOIN attempts a ON a.tenant_id=e.tenant_id AND a.execution_id=e.execution_id
   JOIN sandbox_bindings b ON b.tenant_id=a.tenant_id AND b.attempt_id=a.attempt_id
   WHERE s.tenant_id=$1 AND s.session_id=$2 AND e.execution_id=$3 AND a.attempt_id=$4 AND b.sandbox_binding_id=$8`, tenant, scope.SessionID, scope.ExecutionID, scope.AttemptID, scope.Generation, scope.AttemptOrdinal, old.Request.Deadline, lost).Scan(&safe, &attemptVersion, &sessionVersion)
		if err != nil {
			return err
		}
		if !safe || resources.AttemptID == scope.AttemptID || resources.SandboxID == lost || string(resources.AcquireOperationID) == old.Request.Operation.ID || scope.AttemptOrdinal >= math.MaxInt64 || resources.AttachmentReference == old.Request.Workspace.Reference || resources.BootstrapReference == old.Request.BootstrapReference {
			return sandbox.ErrConflict
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		candidate := old.Request
		candidate.Scope.AttemptID, candidate.Scope.SandboxID = resources.AttemptID, resources.SandboxID
		candidate.Scope.AttemptOrdinal++
		candidate.Operation.ID = string(resources.AcquireOperationID)
		candidate.Workspace.Reference, candidate.BootstrapReference = resources.AttachmentReference, resources.BootstrapReference
		candidate.Operation.Digest, err = sandbox.RequestDigest(candidate)
		if err != nil {
			return err
		}
		if err = s.policy.CheckReplacement(ctx, old, candidate); err != nil {
			return sandbox.ErrPermission
		}
		// ACTIVE permits acquiring compute, not forward work without materialization.
		// Continuity is proven by the restricted no-harness/no-command lane and policy.
		if err = replacementDeadline(ctx, tx, candidate); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET state='ACTIVE',recovery_state=NULL,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2 AND state_version=$3`, tenant, scope.SessionID, sessionVersion)
		if err != nil {
			return err
		}
		// Preserve the legal PENDING/ACQUIRING/STARTING -> INTERRUPTING -> REPLACED edges.
		_, err = tx.ExecContext(ctx, `UPDATE attempts SET state='INTERRUPTING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND attempt_id=$2 AND state_version=$3`, tenant, scope.AttemptID, attemptVersion)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE attempts SET state='REPLACED',is_current=false,terminal_result_reference=$3,terminal_result_digest=$4,terminal_at=clock_timestamp(),updated_at=clock_timestamp(),state_version=state_version+1 WHERE tenant_id=$1 AND attempt_id=$2 AND state_version=$5`, tenant, scope.AttemptID, string(candidate.Scope.AttemptID), candidate.Operation.Digest, attemptVersion+1)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO attempts(tenant_id,attempt_id,execution_id,execution_generation,attempt_no,state) VALUES($1,$2,$3,$4,$5,'PENDING')`, tenant, candidate.Scope.AttemptID, scope.ExecutionID, scope.Generation, candidate.Scope.AttemptOrdinal)
		if err != nil {
			return err
		}
		var operationUsed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_operations WHERE tenant_id=$1 AND operation_id=$2)`, tenant, candidate.Operation.ID).Scan(&operationUsed); err != nil {
			return err
		}
		if operationUsed {
			return sandbox.ErrConflict
		}
		raw, err := sandbox.CanonicalRequest(candidate)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sandbox_bindings(tenant_id,sandbox_binding_id,session_id,execution_id,execution_generation,attempt_id,attempt_no,provider_kind,resolution_digest,acquire_operation_id,acquire_request_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, candidate.Scope.SandboxID, scope.SessionID, scope.ExecutionID, scope.Generation, candidate.Scope.AttemptID, candidate.Scope.AttemptOrdinal, candidate.Profile.Implementation.ProviderKind, candidate.ProfileDigest, candidate.Operation.ID, candidate.Operation.Digest)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sandbox_binding_requests(tenant_id,sandbox_binding_id,acquire_operation_id,canonical_request) VALUES($1,$2,$3,$4)`, tenant, candidate.Scope.SandboxID, candidate.Operation.ID, raw)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE attempts SET sandbox_binding_reference=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND attempt_id=$2`, tenant, candidate.Scope.AttemptID, candidate.Scope.SandboxID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sandbox_replacements(tenant_id,old_sandbox_id,new_sandbox_id) VALUES($1,$2,$3)`, tenant, lost, candidate.Scope.SandboxID)
		if err != nil {
			return err
		}
		// Hand the loss job to the new durable compute job. Respect an existing
		// recovery worker's lease; only unclaimed/expired work can be taken here.
		var recoveryWork primitives.ID
		err = tx.QueryRowContext(ctx, `UPDATE reconciliation_work SET state='CLAIMED',attempts=attempts+1,claim_owner_id=$3,claim_fence=claim_fence+1,claim_expires_at=clock_timestamp()+interval '1 minute',updated_at=clock_timestamp() WHERE tenant_id=$1 AND work_kind='sandbox.recover' AND target_type='sandbox' AND target_id=$2 AND (state='PENDING' OR (state='CLAIMED' AND claim_expires_at<=clock_timestamp())) RETURNING work_id`, tenant, lost, candidate.Scope.AttemptID).Scan(&recoveryWork)
		if errors.Is(err, sql.ErrNoRows) {
			return sandbox.ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE reconciliation_work SET state='COMPLETED',claim_owner_id=NULL,claim_expires_at=NULL,last_error_code=NULL,updated_at=statement_timestamp(),completed_at=statement_timestamp() WHERE tenant_id=$1 AND work_id=$2 AND claim_owner_id=$3`, tenant, recoveryWork, candidate.Scope.AttemptID)
		if err != nil {
			return err
		}
		if err = queueCompute(ctx, tx, tenant, candidate.Scope.SandboxID); err != nil {
			return err
		}
		if err = replacementEvent(ctx, tx, old.Request.Scope, candidate.Scope, "attempt.replaced", attemptVersion+2, now); err != nil {
			return err
		}
		if err = replacementEvent(ctx, tx, old.Request.Scope, candidate.Scope, "session.state_changed", sessionVersion+1, now); err != nil {
			return err
		}
		result = sandbox.Binding{Request: candidate}
		return nil
	})
	if err != nil {
		return sandbox.Binding{}, err
	}
	return result, nil
}

func replacementEvent(ctx context.Context, tx *sql.Tx, old, next sandbox.Scope, kind string, version uint64, now time.Time) error {
	id, err := primitives.NewID(now)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, old.TenantID, old.SessionID); err != nil {
		return err
	}
	var sequence uint64
	if err = tx.QueryRowContext(ctx, `SELECT last_sequence+1 FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, old.TenantID, old.SessionID).Scan(&sequence); err != nil {
		return err
	}
	payload := map[string]any{"old_attempt_id": old.AttemptID, "replacement_attempt_id": next.AttemptID, "old_sandbox_id": old.SandboxID, "replacement_sandbox_id": next.SandboxID, "reason_code": "PRE_EXECUTION_COMPUTE_REPLACED"}
	aggregateType, aggregateID := "attempt", old.AttemptID
	if kind == "session.state_changed" {
		payload["previous_state"], payload["state"] = "DEGRADED", "ACTIVE"
		aggregateType, aggregateID = "session", old.SessionID
	}
	raw, _ := json.Marshal(payload)
	event, err := runtimeevent.New(id, old.TenantID, old.SessionID, old.ExecutionID, old.AttemptID, sequence, version, runtimeevent.Type(kind), now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, raw, runtimeevent.Correlation{}, "runtime-control", nil)
	if err != nil {
		return err
	}
	if err = (*eventRepository)(&repositories{tx: tx, tenantID: old.TenantID}).Append(ctx, event); err != nil {
		return err
	}
	envelope, err := json.Marshal(map[string]any{"schema_version": runtimeevent.SchemaVersion, "event_id": id, "tenant_id": old.TenantID, "session_id": old.SessionID, "execution_id": old.ExecutionID, "attempt_id": old.AttemptID, "sequence": sequence, "aggregate_version": version, "type": kind, "occurred_at": now, "recorded_at": now, "source": "agent-runtime", "classification": "Internal", "payload": payload, "correlation": map[string]string{}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'runtime.events',$3,$2,$4,$5,$6,$7,$8,'PENDING',$9,$9,$9)`, old.TenantID, id, runtimeevent.SchemaVersion, aggregateType, aggregateID, version, envelope, sandbox.Digest(envelope), now)
	return err
}

func replacementDeadline(ctx context.Context, tx *sql.Tx, r sandbox.AcquireRequest) error {
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT deadline>clock_timestamp() AND $3::timestamptz>clock_timestamp() FROM executions WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID, r.Deadline).Scan(&current); err != nil {
		return err
	}
	if !current {
		return sandbox.ErrConflict
	}
	return nil
}
