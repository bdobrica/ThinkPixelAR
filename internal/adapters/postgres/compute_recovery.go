package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// recordComputeRecovery shares the fenced observation transaction. It creates
// work for recovery, never terminalizes or automatically retries an Execution.
func recordComputeRecovery(ctx context.Context, tx *sql.Tx, intent sandbox.ComputeIntent, observed sandbox.ComputeObservation, now time.Time) error {
	scope := intent.Binding.Request.Scope
	recoveryID, err := primitives.NewID(now)
	if err != nil {
		return err
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO reconciliation_work(tenant_id,work_id,work_kind,target_type,target_id,state,next_attempt_at,created_at,updated_at,last_error_code) VALUES($1,$2,'sandbox.recover','sandbox',$3,'PENDING',$4,$4,$4,$5) ON CONFLICT(tenant_id,work_kind,target_type,target_id) DO NOTHING`, scope.TenantID, recoveryID, scope.SandboxID, now, observed.Code)
	if err != nil {
		return err
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if intent.Desired == sandbox.ComputeReleased {
		// An exact release already exists, but ownership/effective-state verification
		// failed. Quarantine it rather than repeatedly deleting an uncertain target.
		_, err = tx.ExecContext(ctx, `UPDATE cleanup_intents SET state='QUARANTINED',state_version=state_version+1,attempts=attempts+1,last_error_code='PROVIDER_INTEGRITY_FAILURE',quarantined_at=$3,updated_at=$3 WHERE tenant_id=$1 AND cleanup_operation_id=$2 AND state='PENDING'`, scope.TenantID, intent.Operation.ID, now)
		if err != nil || count == 0 {
			return err
		}
		return recordRecoveryEvent(ctx, tx, intent, observed.Code, "sandbox.health_changed", intent.Version+1, now)
	}
	if count == 0 {
		return nil
	}
	// Only this still-current Attempt may degrade the associated active Session.
	// An obsolete binding never changes a newer Attempt/Execution/Session epoch.
	var version uint64
	err = tx.QueryRowContext(ctx, `UPDATE sessions s SET state='DEGRADED',recovery_state='ACTIVE',state_version=state_version+1,updated_at=$6 WHERE tenant_id=$1 AND session_id=$2 AND state='ACTIVE' AND current_execution_id=$3 AND execution_generation=$4 AND EXISTS(SELECT 1 FROM attempts a JOIN executions e ON e.tenant_id=a.tenant_id AND e.execution_id=a.execution_id WHERE a.tenant_id=$1 AND a.attempt_id=$5 AND a.is_current AND e.state IN ('MATERIALIZING','RUNNING')) RETURNING state_version`, scope.TenantID, scope.SessionID, scope.ExecutionID, scope.Generation, scope.AttemptID, now).Scan(&version)
	eventType := "session.degraded"
	if errors.Is(err, sql.ErrNoRows) {
		version = intent.Version + 1
		eventType = "sandbox.health_changed"
	} else if err != nil {
		return err
	}
	if err = recordRecoveryEvent(ctx, tx, intent, observed.Code, eventType, version, now); err != nil {
		return err
	}

	if intent.Binding.ProviderReference == "" {
		return nil
	} // No exact UID evidence: recovery must resolve ambiguity.
	operationID, err := primitives.NewID(now)
	if err != nil {
		return err
	}
	digest := sandbox.LifecycleDigest(scope.TenantID, scope.SandboxID, "release", string(operationID))
	_, err = tx.ExecContext(ctx, `INSERT INTO cleanup_intents(tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox-binding',$3,'sandbox',$4,$5,$2,$6,$7,$8,'PENDING',$9,$9,$9)`, scope.TenantID, operationID, scope.SandboxID, intent.Binding.Request.Profile.Implementation.ProviderKind, intent.Binding.ProviderReference, digest, intent.Binding.Request.Operation.Digest, !intent.Current, now)
	if err != nil {
		return err
	}
	revision := intent.Revision + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO sandbox_operations(tenant_id,sandbox_binding_id,operation_id,kind,request_digest,revision) VALUES($1,$2,$3,'release',$4,$5)`, scope.TenantID, scope.SandboxID, operationID, digest, revision)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sandbox_binding_requests SET operation_revision=$3 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID, revision)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET release_operation_id=$3,release_request_digest=$4,state_version=state_version+1,updated_at=$5 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID, operationID, digest, now)
	return err
}

func recordRecoveryEvent(ctx context.Context, tx *sql.Tx, intent sandbox.ComputeIntent, code, eventType string, version uint64, now time.Time) error {
	scope := intent.Binding.Request.Scope
	eventID, err := primitives.NewID(now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, scope.TenantID, scope.SessionID)
	if err != nil {
		return err
	}
	var sequence uint64
	if err = tx.QueryRowContext(ctx, `SELECT last_sequence+1 FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, scope.TenantID, scope.SessionID).Scan(&sequence); err != nil {
		return err
	}
	payload := map[string]any{"sandbox_id": scope.SandboxID, "reason_code": code, "recovery_required": true}
	if eventType == "session.degraded" {
		payload["previous_state"] = "ACTIVE"
		payload["state"] = "DEGRADED"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event, err := runtimeevent.New(eventID, scope.TenantID, scope.SessionID, scope.ExecutionID, scope.AttemptID, sequence, version, runtimeevent.Type(eventType), now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, raw, runtimeevent.Correlation{}, "runtime-control", nil)
	if err != nil {
		return err
	}
	if err = (*eventRepository)(&repositories{tx: tx, tenantID: scope.TenantID}).Append(ctx, event); err != nil {
		return err
	}
	aggregateType, aggregateID := "session", scope.SessionID
	if eventType == "sandbox.health_changed" {
		aggregateType, aggregateID = "sandbox-binding", scope.SandboxID
	}
	envelope, err := json.Marshal(map[string]any{"schema_version": runtimeevent.SchemaVersion, "event_id": eventID, "tenant_id": scope.TenantID, "session_id": scope.SessionID, "execution_id": scope.ExecutionID, "attempt_id": scope.AttemptID, "sequence": sequence, "aggregate_version": version, "type": eventType, "occurred_at": now, "recorded_at": now, "source": "agent-runtime", "classification": "Internal", "payload": payload, "correlation": map[string]string{}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'runtime.events',$3,$2,$9,$4,$5,$6,$7,'PENDING',$8,$8,$8)`, scope.TenantID, eventID, runtimeevent.SchemaVersion, aggregateID, version, envelope, sandbox.Digest(envelope), now, aggregateType)
	return err
}
