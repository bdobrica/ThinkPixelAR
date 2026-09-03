package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/cleanup"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"github.com/jackc/pgx/v5/pgconn"
)

// Store implements tenant-scoped repositories over PostgreSQL.
type Store struct{ db *sql.DB }

var _ persistence.TransactionManager = (*Store)(nil)

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("postgres database is required")
	}
	return &Store{db: db}, nil
}

func (s *Store) WithinTransaction(ctx context.Context, tenantID primitives.ID, work func(context.Context, persistence.Repositories) error) error {
	if _, err := primitives.ParseID(string(tenantID)); err != nil {
		return fmt.Errorf("tenant context: %w", err)
	}
	if work == nil {
		return errors.New("transaction callback is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	repos := &repositories{tx: tx, tenantID: tenantID}
	if err = work(ctx, repos); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type repositories struct {
	tx       *sql.Tx
	tenantID primitives.ID
}

func (r *repositories) Sessions() persistence.SessionRepository     { return (*sessionRepository)(r) }
func (r *repositories) Executions() persistence.ExecutionRepository { return (*executionRepository)(r) }
func (r *repositories) Attempts() persistence.AttemptRepository     { return (*attemptRepository)(r) }
func (r *repositories) RuntimeEvents() persistence.RuntimeEventRepository {
	return (*eventRepository)(r)
}
func (r *repositories) Idempotency() persistence.IdempotencyRepository {
	return (*idempotencyRepository)(r)
}
func (r *repositories) Outbox() persistence.OutboxRepository { return (*outboxRepository)(r) }
func (r *repositories) Reconciliation() persistence.ReconciliationRepository {
	return (*reconciliationRepository)(r)
}
func (r *repositories) Cleanup() persistence.CleanupRepository { return (*cleanupRepository)(r) }
func (r *repositories) owns(id primitives.ID) error {
	if id != r.tenantID {
		return errors.New("aggregate tenant does not match transaction tenant")
	}
	return nil
}

type sessionRepository repositories

func (r *sessionRepository) Add(ctx context.Context, value *session.Session) error {
	if value == nil {
		return errors.New("session is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	b := value.Binding()
	closed := nullableTime(value.ClosedAt())
	_, err := r.tx.ExecContext(ctx, `INSERT INTO sessions (tenant_id,session_id,state,state_version,execution_generation,authority_mode,authority_namespace,agent_id,agent_version_id,runtime_spec_schema_version,runtime_spec,runtime_spec_digest,runtime_profile_schema_version,runtime_profile_snapshot,runtime_profile_digest,recovery_state,created_at,updated_at,closed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, value.TenantID(), value.ID(), value.State(), value.StateVersion(), value.ExecutionGeneration(), b.AuthorityMode, b.AuthorityNamespace, b.AgentID, b.AgentVersionID, b.RuntimeSpecSchemaVersion, b.RuntimeSpec, b.RuntimeSpecDigest, b.RuntimeProfileSchemaVersion, b.RuntimeProfileSnapshot, b.RuntimeProfileDigest, nullString(string(value.RecoveryState())), value.CreatedAt(), value.UpdatedAt(), closed)
	return wrap("insert session", err)
}
func (r *sessionRepository) Get(ctx context.Context, id primitives.ID) (*session.Session, error) {
	var state, recovery string
	var version, generation uint64
	var b session.RuntimeBinding
	var created, updated sql.NullTime
	var closed sql.NullTime
	err := r.tx.QueryRowContext(ctx, `SELECT state,state_version,execution_generation,authority_mode,authority_namespace,agent_id,agent_version_id,runtime_spec_schema_version,runtime_spec,runtime_spec_digest,runtime_profile_schema_version,runtime_profile_snapshot,runtime_profile_digest,COALESCE(recovery_state,''),created_at,updated_at,closed_at FROM sessions WHERE tenant_id=$1 AND session_id=$2`, r.tenantID, id).Scan(&state, &version, &generation, &b.AuthorityMode, &b.AuthorityNamespace, &b.AgentID, &b.AgentVersionID, &b.RuntimeSpecSchemaVersion, &b.RuntimeSpec, &b.RuntimeSpecDigest, &b.RuntimeProfileSchemaVersion, &b.RuntimeProfileSnapshot, &b.RuntimeProfileDigest, &recovery, &created, &updated, &closed)
	if err != nil {
		return nil, wrap("get session", err)
	}
	parsed, err := session.ParseState(state)
	if err != nil {
		return nil, err
	}
	var recoveryState session.State
	if recovery != "" {
		recoveryState, err = session.ParseState(recovery)
		if err != nil {
			return nil, err
		}
	}
	return session.Restore(r.tenantID, id, b, parsed, recoveryState, version, generation, created.Time, updated.Time, timePtr(closed))
}
func (r *sessionRepository) Update(ctx context.Context, value *session.Session, expected uint64) error {
	if value == nil {
		return errors.New("session is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	closed := nullableTime(value.ClosedAt())
	result, err := r.tx.ExecContext(ctx, `UPDATE sessions SET state=$3,state_version=$4,execution_generation=$5,recovery_state=$6,updated_at=$7,closed_at=$8 WHERE tenant_id=$1 AND session_id=$2 AND state_version=$9`, r.tenantID, value.ID(), value.State(), value.StateVersion(), value.ExecutionGeneration(), nullString(string(value.RecoveryState())), value.UpdatedAt(), closed, expected)
	return affected("update session", result, err)
}

type executionRepository repositories

func (r *executionRepository) Add(ctx context.Context, value *execution.Execution) error {
	if value == nil {
		return errors.New("execution is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	b := value.Binding()
	tr, td, ta := executionTerminal(value)
	_, err := r.tx.ExecContext(ctx, `INSERT INTO executions (tenant_id,execution_id,session_id,session_generation,authority_mode,authority_namespace,authority_reference,external_run_id,grant_digest,agent_id,agent_version_id,agent_evidence,agent_evidence_digest,deadline,state,state_version,terminal_result_reference,terminal_result_digest,created_at,updated_at,terminal_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, value.TenantID(), value.ID(), b.SessionID, b.SessionGeneration, b.AuthorityMode, b.AuthorityNamespace, b.AuthorityReference, nullString(b.ExternalRunID), b.GrantDigest, b.AgentID, b.AgentVersionID, b.AgentEvidence, b.AgentEvidenceDigest, value.Deadline(), value.State(), value.StateVersion(), tr, td, value.CreatedAt(), value.UpdatedAt(), ta)
	return wrap("insert execution", err)
}
func (r *executionRepository) Get(ctx context.Context, id primitives.ID) (*execution.Execution, error) {
	var b execution.Binding
	var deadline, created, updated time.Time
	var state string
	var version uint64
	var ext, ref, digest sql.NullString
	var terminal sql.NullTime
	err := r.tx.QueryRowContext(ctx, `SELECT session_id,session_generation,authority_mode,authority_namespace,authority_reference,external_run_id,grant_digest,agent_id,agent_version_id,agent_evidence,agent_evidence_digest,deadline,state,state_version,terminal_result_reference,terminal_result_digest,created_at,updated_at,terminal_at FROM executions WHERE tenant_id=$1 AND execution_id=$2`, r.tenantID, id).Scan(&b.SessionID, &b.SessionGeneration, &b.AuthorityMode, &b.AuthorityNamespace, &b.AuthorityReference, &ext, &b.GrantDigest, &b.AgentID, &b.AgentVersionID, &b.AgentEvidence, &b.AgentEvidenceDigest, &deadline, &state, &version, &ref, &digest, &created, &updated, &terminal)
	if err != nil {
		return nil, wrap("get execution", err)
	}
	b.ExternalRunID = ext.String
	parsed, err := execution.ParseState(state)
	if err != nil {
		return nil, err
	}
	var tr *execution.TerminalResult
	if ref.Valid {
		tr = &execution.TerminalResult{Reference: ref.String, Digest: digest.String}
	}
	return execution.Restore(r.tenantID, id, b, deadline, parsed, version, tr, created, updated, timePtr(terminal))
}
func (r *executionRepository) Update(ctx context.Context, value *execution.Execution, expected uint64) error {
	if value == nil {
		return errors.New("execution is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	ref, digest, terminal := executionTerminal(value)
	result, err := r.tx.ExecContext(ctx, `UPDATE executions SET state=$3,state_version=$4,terminal_result_reference=$5,terminal_result_digest=$6,updated_at=$7,terminal_at=$8 WHERE tenant_id=$1 AND execution_id=$2 AND state_version=$9`, r.tenantID, value.ID(), value.State(), value.StateVersion(), ref, digest, value.UpdatedAt(), terminal, expected)
	return affected("update execution", result, err)
}

type attemptRepository repositories

func (r *attemptRepository) Add(ctx context.Context, value *attempt.Attempt) error {
	if value == nil {
		return errors.New("attempt is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	b := value.Binding()
	sr := nullableID(value.SandboxReference())
	hr := nullableID(value.HarnessReference())
	sh := nullableTime(value.SandboxHeartbeatAt())
	hh := nullableTime(value.HarnessHeartbeatAt())
	ref, digest, terminal := attemptTerminal(value)
	_, err := r.tx.ExecContext(ctx, `INSERT INTO attempts (tenant_id,attempt_id,execution_id,execution_generation,attempt_no,is_current,state,state_version,sandbox_binding_reference,harness_binding_reference,sandbox_heartbeat_at,harness_heartbeat_at,terminal_result_reference,terminal_result_digest,created_at,updated_at,terminal_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, value.TenantID(), value.ID(), b.ExecutionID, b.ExecutionGeneration, b.Number, value.IsCurrent(), value.State(), value.StateVersion(), sr, hr, sh, hh, ref, digest, value.CreatedAt(), value.UpdatedAt(), terminal)
	return wrap("insert attempt", err)
}
func (r *attemptRepository) Get(ctx context.Context, id primitives.ID) (*attempt.Attempt, error) {
	var b attempt.Binding
	var current bool
	var state string
	var version uint64
	var sr, hr sql.NullString
	var sh, hh, terminal sql.NullTime
	var ref, digest sql.NullString
	var created, updated time.Time
	err := r.tx.QueryRowContext(ctx, `SELECT execution_id,execution_generation,attempt_no,is_current,state,state_version,sandbox_binding_reference,harness_binding_reference,sandbox_heartbeat_at,harness_heartbeat_at,terminal_result_reference,terminal_result_digest,created_at,updated_at,terminal_at FROM attempts WHERE tenant_id=$1 AND attempt_id=$2`, r.tenantID, id).Scan(&b.ExecutionID, &b.ExecutionGeneration, &b.Number, &current, &state, &version, &sr, &hr, &sh, &hh, &ref, &digest, &created, &updated, &terminal)
	if err != nil {
		return nil, wrap("get attempt", err)
	}
	parsed, err := attempt.ParseState(state)
	if err != nil {
		return nil, err
	}
	var tr *attempt.TerminalResult
	if ref.Valid {
		tr = &attempt.TerminalResult{Reference: ref.String, Digest: digest.String}
	}
	return attempt.Restore(r.tenantID, id, b, parsed, version, current, primitives.ID(sr.String), primitives.ID(hr.String), timePtr(sh), timePtr(hh), tr, created, updated, timePtr(terminal))
}
func (r *attemptRepository) Update(ctx context.Context, value *attempt.Attempt, expected uint64) error {
	if value == nil {
		return errors.New("attempt is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	sr := nullableID(value.SandboxReference())
	hr := nullableID(value.HarnessReference())
	sh := nullableTime(value.SandboxHeartbeatAt())
	hh := nullableTime(value.HarnessHeartbeatAt())
	ref, digest, terminal := attemptTerminal(value)
	result, err := r.tx.ExecContext(ctx, `UPDATE attempts SET is_current=$3,state=$4,state_version=$5,sandbox_binding_reference=$6,harness_binding_reference=$7,sandbox_heartbeat_at=$8,harness_heartbeat_at=$9,terminal_result_reference=$10,terminal_result_digest=$11,updated_at=$12,terminal_at=$13 WHERE tenant_id=$1 AND attempt_id=$2 AND state_version=$14`, r.tenantID, value.ID(), value.IsCurrent(), value.State(), value.StateVersion(), sr, hr, sh, hh, ref, digest, value.UpdatedAt(), terminal, expected)
	return affected("update attempt", result, err)
}

type eventRepository repositories

func (r *eventRepository) Append(ctx context.Context, value *runtimeevent.Event) error {
	if value == nil {
		return errors.New("runtime event is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	ret := nullableTime(value.RetainUntil())
	c := value.Correlation()
	_, err := r.tx.ExecContext(ctx, `INSERT INTO runtime_events (tenant_id,event_id,session_id,execution_id,attempt_id,sequence,aggregate_version,schema_version,event_type,occurred_at,recorded_at,source,classification,payload,request_id,trace_id,span_id,retention_policy,retain_until) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, value.TenantID(), value.EventID(), value.SessionID(), nullID(value.ExecutionID()), nullID(value.AttemptID()), value.Sequence(), value.AggregateVersion(), runtimeevent.SchemaVersion, value.Type(), value.OccurredAt(), value.RecordedAt(), value.Source(), value.Classification(), value.Payload(), nullID(c.RequestID), nullString(c.TraceID), nullString(c.SpanID), value.RetentionPolicy(), ret)
	return wrap("append runtime event", err)
}

type idempotencyRepository repositories

func (r *idempotencyRepository) Reserve(ctx context.Context, value *idempotency.Record) (*idempotency.Record, bool, error) {
	if value == nil {
		return nil, false, errors.New("idempotency record is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return nil, false, err
	}
	s := value.Scope()
	lease, _ := value.LeaseExpiresAt()
	result, err := r.tx.ExecContext(ctx, `INSERT INTO idempotency_records (tenant_id,idempotency_record_id,principal_digest,action,key_digest,normalization_version,request_digest,operation_id,resource_id,state,owner_id,owner_fence,lease_expires_at,audit_correlation_id,created_at,updated_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT (tenant_id,principal_digest,action,key_digest) DO NOTHING`, value.TenantID(), value.ID(), s.PrincipalDigest, s.Action, s.KeyDigest, value.NormalizationVersion(), value.RequestDigest(), value.OperationID(), nullID(value.ResourceID()), value.State(), value.OwnerID(), value.OwnerFence(), lease, value.AuditCorrelationID(), value.CreatedAt(), value.UpdatedAt(), value.ExpiresAt())
	if err != nil {
		return nil, false, wrap("reserve idempotency record", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, false, wrap("reserve idempotency record", err)
	}
	if created == 1 {
		return value, true, nil
	}
	existing, err := r.get(ctx, s, true)
	if err != nil {
		return nil, false, err
	}
	if existing.NormalizationVersion() != value.NormalizationVersion() || existing.RequestDigest() != value.RequestDigest() {
		return nil, false, persistence.ErrRequestDigestMismatch
	}
	return existing, false, nil
}

func (r *idempotencyRepository) Get(ctx context.Context, scope idempotency.Scope) (*idempotency.Record, error) {
	return r.get(ctx, scope, false)
}

func (r *idempotencyRepository) get(ctx context.Context, scope idempotency.Scope, lock bool) (*idempotency.Record, error) {
	query := `SELECT idempotency_record_id,normalization_version,request_digest,operation_id,resource_id,state,owner_id,owner_fence,lease_expires_at,http_status,response_payload,response_reference,problem_type,problem_code,audit_correlation_id,created_at,updated_at,completed_at,expires_at FROM idempotency_records WHERE tenant_id=$1 AND principal_digest=$2 AND action=$3 AND key_digest=$4`
	if lock {
		query += ` FOR UPDATE`
	}
	var id, operation, owner, audit primitives.ID
	var resource sql.NullString
	var state string
	var fence uint64
	var normalization, digest string
	var lease, completed sql.NullTime
	var status sql.NullInt64
	var payload []byte
	var reference, problemType, problemCode sql.NullString
	var created, updated, expires time.Time
	err := r.tx.QueryRowContext(ctx, query, r.tenantID, scope.PrincipalDigest, scope.Action, scope.KeyDigest).Scan(&id, &normalization, &digest, &operation, &resource, &state, &owner, &fence, &lease, &status, &payload, &reference, &problemType, &problemCode, &audit, &created, &updated, &completed, &expires)
	if err != nil {
		return nil, wrap("get idempotency record", err)
	}
	var response *idempotency.Response
	var failure *idempotency.Failure
	if state == string(idempotency.Succeeded) {
		response = &idempotency.Response{HTTPStatus: int(status.Int64), Payload: payload, Reference: reference.String}
	}
	if state == string(idempotency.Failed) {
		failure = &idempotency.Failure{HTTPStatus: int(status.Int64), ProblemType: problemType.String, ProblemCode: problemCode.String}
	}
	return idempotency.Restore(r.tenantID, id, scope, normalization, digest, operation, primitives.ID(resource.String), owner, audit, idempotency.State(state), fence, timePtr(lease), response, failure, created, updated, timePtr(completed), expires)
}

func (r *idempotencyRepository) Update(ctx context.Context, value *idempotency.Record, expectedFence uint64) error {
	if value == nil {
		return errors.New("idempotency record is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	var status, payload, reference, problemType, problemCode any
	if response, ok := value.Response(); ok {
		status = response.HTTPStatus
		if response.Payload != nil {
			payload = response.Payload
		}
		reference = nullString(response.Reference)
	}
	if failure, ok := value.Failure(); ok {
		status = failure.HTTPStatus
		problemType = failure.ProblemType
		problemCode = failure.ProblemCode
	}
	lease, hasLease := value.LeaseExpiresAt()
	completed, hasCompleted := value.CompletedAt()
	result, err := r.tx.ExecContext(ctx, `UPDATE idempotency_records SET state=$3,owner_id=$4,owner_fence=$5,lease_expires_at=$6,http_status=$7,response_payload=$8,response_reference=$9,problem_type=$10,problem_code=$11,updated_at=$12,completed_at=$13 WHERE tenant_id=$1 AND idempotency_record_id=$2 AND owner_fence=$14 AND state='IN_PROGRESS'`, r.tenantID, value.ID(), value.State(), value.OwnerID(), value.OwnerFence(), nullableTime(lease, hasLease), status, payload, reference, problemType, problemCode, value.UpdatedAt(), nullableTime(completed, hasCompleted), expectedFence)
	return affected("update idempotency record", result, err)
}

func (r *idempotencyRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
	if before.IsZero() || limit < 1 || limit > 1000 {
		return 0, errors.New("invalid idempotency expiry request")
	}
	result, err := r.tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE (tenant_id,idempotency_record_id) IN (SELECT tenant_id,idempotency_record_id FROM idempotency_records WHERE tenant_id=$1 AND state<>'IN_PROGRESS' AND expires_at<=$2 ORDER BY expires_at,idempotency_record_id LIMIT $3 FOR UPDATE SKIP LOCKED)`, r.tenantID, before.UTC(), limit)
	if err != nil {
		return 0, wrap("delete expired idempotency records", err)
	}
	return result.RowsAffected()
}

type outboxRepository repositories

func (r *outboxRepository) Add(ctx context.Context, value *outbox.Message) error {
	if value == nil {
		return errors.New("outbox message is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	e := value.Envelope()
	var payload any
	if len(e.Payload) > 0 {
		payload = e.Payload
	}
	_, err := r.tx.ExecContext(ctx, `INSERT INTO outbox_messages (tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_reference,payload_digest,state,attempts,claim_fence,available_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, value.TenantID(), value.ID(), e.Topic, e.SchemaVersion, e.EventID, e.AggregateType, e.AggregateID, e.AggregateVersion, payload, nullString(e.PayloadReference), e.PayloadDigest, value.State(), value.Attempts(), value.ClaimFence(), value.AvailableAt(), value.CreatedAt(), value.UpdatedAt())
	return wrap("insert outbox message", err)
}

func (r *outboxRepository) Get(ctx context.Context, id primitives.ID) (*outbox.Message, error) {
	row := r.tx.QueryRowContext(ctx, outboxSelect+` WHERE tenant_id=$1 AND message_id=$2`, r.tenantID, id)
	return scanOutbox(row, r.tenantID)
}

func (r *outboxRepository) ClaimAvailable(ctx context.Context, ownerID primitives.ID, now, leaseExpiresAt time.Time, limit int) ([]*outbox.Message, error) {
	if _, err := primitives.ParseID(string(ownerID)); err != nil || now.IsZero() || !leaseExpiresAt.After(now) || limit < 1 || limit > 100 {
		return nil, errors.New("invalid outbox claim request")
	}
	rows, err := r.tx.QueryContext(ctx, `WITH candidates AS (
SELECT tenant_id,message_id FROM outbox_messages
WHERE tenant_id=$1 AND available_at<=$2 AND (state='PENDING' OR (state='CLAIMED' AND claim_expires_at<=$2))
ORDER BY available_at,message_id LIMIT $3 FOR UPDATE SKIP LOCKED
), claimed AS (
UPDATE outbox_messages AS o SET state='CLAIMED',attempts=o.attempts+1,claim_owner_id=$4,claim_fence=o.claim_fence+1,claim_expires_at=$5,updated_at=$2
FROM candidates AS c WHERE o.tenant_id=c.tenant_id AND o.message_id=c.message_id RETURNING o.*
)
SELECT message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_reference,payload_digest,state,attempts,claim_owner_id,claim_fence,available_at,claim_expires_at,last_error_code,dead_letter_reason_code,dead_letter_detail,created_at,updated_at,delivered_at FROM claimed ORDER BY available_at,message_id`, r.tenantID, now.UTC(), limit, ownerID, leaseExpiresAt.UTC())
	if err != nil {
		return nil, wrap("claim outbox messages", err)
	}
	defer rows.Close()
	messages := make([]*outbox.Message, 0, limit)
	for rows.Next() {
		message, err := scanOutbox(rows, r.tenantID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err = rows.Err(); err != nil {
		return nil, wrap("claim outbox messages", err)
	}
	return messages, nil
}

func (r *outboxRepository) Update(ctx context.Context, value *outbox.Message, expectedFence uint64) error {
	if value == nil {
		return errors.New("outbox message is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	claimExpiry, hasClaim := value.ClaimExpiresAt()
	delivered, hasDelivered := value.DeliveredAt()
	var reason, detail any
	if dead, ok := value.DeadLetterMetadata(); ok {
		reason, detail = dead.ReasonCode, dead.Detail
	}
	result, err := r.tx.ExecContext(ctx, `UPDATE outbox_messages SET state=$3,attempts=$4,claim_owner_id=$5,claim_fence=$6,claim_expires_at=$7,available_at=$8,last_error_code=$9,dead_letter_reason_code=$10,dead_letter_detail=$11,updated_at=$12,delivered_at=$13 WHERE tenant_id=$1 AND message_id=$2 AND state='CLAIMED' AND claim_fence=$14`, r.tenantID, value.ID(), value.State(), value.Attempts(), nullID(value.OwnerID()), value.ClaimFence(), nullableTime(claimExpiry, hasClaim), value.AvailableAt(), nullString(value.LastErrorCode()), reason, detail, value.UpdatedAt(), nullableTime(delivered, hasDelivered), expectedFence)
	return affected("update outbox message", result, err)
}

const outboxSelect = `SELECT message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_reference,payload_digest,state,attempts,claim_owner_id,claim_fence,available_at,claim_expires_at,last_error_code,dead_letter_reason_code,dead_letter_detail,created_at,updated_at,delivered_at FROM outbox_messages`

type rowScanner interface{ Scan(...any) error }

func scanOutbox(row rowScanner, tenantID primitives.ID) (*outbox.Message, error) {
	var id, eventID, aggregateID primitives.ID
	var envelope outbox.Envelope
	var state string
	var attempts, fence uint64
	var payload []byte
	var payloadRef, owner, lastError, reason, detail sql.NullString
	var available, created, updated time.Time
	var claimExpiry, delivered sql.NullTime
	err := row.Scan(&id, &envelope.Topic, &envelope.SchemaVersion, &eventID, &envelope.AggregateType, &aggregateID, &envelope.AggregateVersion, &payload, &payloadRef, &envelope.PayloadDigest, &state, &attempts, &owner, &fence, &available, &claimExpiry, &lastError, &reason, &detail, &created, &updated, &delivered)
	if err != nil {
		return nil, wrap("scan outbox message", err)
	}
	envelope.EventID, envelope.AggregateID, envelope.Payload, envelope.PayloadReference = eventID, aggregateID, payload, payloadRef.String
	var dead *outbox.DeadLetter
	if reason.Valid {
		dead = &outbox.DeadLetter{ReasonCode: reason.String, Detail: detail.String}
	}
	return outbox.Restore(tenantID, id, envelope, outbox.State(state), attempts, fence, primitives.ID(owner.String), available, timePtr(claimExpiry), lastError.String, dead, created, updated, timePtr(delivered))
}

type reconciliationRepository repositories

func (r *reconciliationRepository) Add(ctx context.Context, value *reconciliation.Work) error {
	if value == nil {
		return errors.New("reconciliation work is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	_, err := r.tx.ExecContext(ctx, `INSERT INTO reconciliation_work (tenant_id,work_id,work_kind,target_type,target_id,state,attempts,claim_fence,next_attempt_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, value.TenantID(), value.ID(), value.Kind(), value.TargetType(), value.TargetID(), value.State(), value.Attempts(), value.ClaimFence(), value.NextAttemptAt(), value.CreatedAt(), value.UpdatedAt())
	return wrap("insert reconciliation work", err)
}

func (r *reconciliationRepository) Get(ctx context.Context, id primitives.ID) (*reconciliation.Work, error) {
	return scanReconciliation(r.tx.QueryRowContext(ctx, reconciliationSelect+` WHERE tenant_id=$1 AND work_id=$2`, r.tenantID, id), r.tenantID)
}

func (r *reconciliationRepository) ClaimAvailable(ctx context.Context, ownerID primitives.ID, now, leaseExpiresAt time.Time, limit int) ([]*reconciliation.Work, error) {
	if _, err := primitives.ParseID(string(ownerID)); err != nil || now.IsZero() || !leaseExpiresAt.After(now) || limit < 1 || limit > 100 {
		return nil, errors.New("invalid reconciliation claim request")
	}
	rows, err := r.tx.QueryContext(ctx, `WITH candidates AS (
SELECT tenant_id,work_id FROM reconciliation_work WHERE tenant_id=$1 AND next_attempt_at<=$2
AND (state='PENDING' OR (state='CLAIMED' AND claim_expires_at<=$2))
ORDER BY next_attempt_at,work_id LIMIT $3 FOR UPDATE SKIP LOCKED
), claimed AS (
UPDATE reconciliation_work AS w SET state='CLAIMED',attempts=w.attempts+1,claim_owner_id=$4,
claim_fence=w.claim_fence+1,claim_expires_at=$5,updated_at=$2 FROM candidates AS c
WHERE w.tenant_id=c.tenant_id AND w.work_id=c.work_id RETURNING w.*
)
SELECT work_id,work_kind,target_type,target_id,state,attempts,claim_owner_id,claim_fence,next_attempt_at,claim_expires_at,last_error_code,created_at,updated_at,completed_at FROM claimed ORDER BY next_attempt_at,work_id`, r.tenantID, now.UTC(), limit, ownerID, leaseExpiresAt.UTC())
	if err != nil {
		return nil, wrap("claim reconciliation work", err)
	}
	defer rows.Close()
	result := make([]*reconciliation.Work, 0, limit)
	for rows.Next() {
		value, err := scanReconciliation(rows, r.tenantID)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, wrap("claim reconciliation work", err)
	}
	return result, nil
}

func (r *reconciliationRepository) Update(ctx context.Context, value *reconciliation.Work, expectedFence uint64) error {
	if value == nil {
		return errors.New("reconciliation work is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	claim, hasClaim := value.ClaimExpiresAt()
	completed, hasCompleted := value.CompletedAt()
	result, err := r.tx.ExecContext(ctx, `UPDATE reconciliation_work SET state=$3,attempts=$4,claim_owner_id=$5,claim_fence=$6,claim_expires_at=$7,next_attempt_at=$8,last_error_code=$9,updated_at=$10,completed_at=$11 WHERE tenant_id=$1 AND work_id=$2 AND state='CLAIMED' AND claim_fence=$12`, r.tenantID, value.ID(), value.State(), value.Attempts(), nullID(value.OwnerID()), value.ClaimFence(), nullableTime(claim, hasClaim), value.NextAttemptAt(), nullString(value.LastErrorCode()), value.UpdatedAt(), nullableTime(completed, hasCompleted), expectedFence)
	return affected("update reconciliation work", result, err)
}

const reconciliationSelect = `SELECT work_id,work_kind,target_type,target_id,state,attempts,claim_owner_id,claim_fence,next_attempt_at,claim_expires_at,last_error_code,created_at,updated_at,completed_at FROM reconciliation_work`

func scanReconciliation(row rowScanner, tenantID primitives.ID) (*reconciliation.Work, error) {
	var id, targetID primitives.ID
	var kind, targetType, state string
	var attempts, fence uint64
	var owner, lastError sql.NullString
	var next, created, updated time.Time
	var claim, completed sql.NullTime
	if err := row.Scan(&id, &kind, &targetType, &targetID, &state, &attempts, &owner, &fence, &next, &claim, &lastError, &created, &updated, &completed); err != nil {
		return nil, wrap("scan reconciliation work", err)
	}
	return reconciliation.Restore(tenantID, id, kind, targetType, targetID, reconciliation.State(state), attempts, fence, primitives.ID(owner.String), next, timePtr(claim), lastError.String, created, updated, timePtr(completed))
}

type cleanupRepository repositories

func (r *cleanupRepository) Add(ctx context.Context, value *cleanup.Intent) error {
	if value == nil {
		return errors.New("cleanup intent is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	t := value.Target()
	_, err := r.tx.ExecContext(ctx, `INSERT INTO cleanup_intents (tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,state_version,attempts,next_attempt_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, value.TenantID(), value.ID(), t.OwnerType, t.OwnerID, t.TargetType, t.ProviderKind, t.ExternalReference, t.OperationID, t.RequestDigest, t.OwnershipProofDigest, t.Orphan, value.State(), value.Version(), value.Attempts(), value.NextAttemptAt(), value.CreatedAt(), value.UpdatedAt())
	return wrap("insert cleanup intent", err)
}

func (r *cleanupRepository) Get(ctx context.Context, id primitives.ID) (*cleanup.Intent, error) {
	return scanCleanup(r.tx.QueryRowContext(ctx, cleanupSelect+` WHERE tenant_id=$1 AND cleanup_intent_id=$2`, r.tenantID, id), r.tenantID)
}

func (r *cleanupRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*cleanup.Intent, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, errors.New("invalid cleanup query")
	}
	rows, err := r.tx.QueryContext(ctx, cleanupSelect+` WHERE tenant_id=$1 AND state='PENDING' AND next_attempt_at<=$2 ORDER BY next_attempt_at,cleanup_intent_id LIMIT $3`, r.tenantID, now.UTC(), limit)
	if err != nil {
		return nil, wrap("list due cleanup intents", err)
	}
	defer rows.Close()
	result := make([]*cleanup.Intent, 0, limit)
	for rows.Next() {
		value, scanErr := scanCleanup(rows, r.tenantID)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, wrap("list due cleanup intents", err)
	}
	return result, nil
}

func (r *cleanupRepository) Update(ctx context.Context, value *cleanup.Intent, expectedVersion uint64) error {
	if value == nil {
		return errors.New("cleanup intent is required")
	}
	if err := (*repositories)(r).owns(value.TenantID()); err != nil {
		return err
	}
	confirmed, hasConfirmed := value.ConfirmedAt()
	quarantined, hasQuarantined := value.QuarantinedAt()
	result, err := r.tx.ExecContext(ctx, `UPDATE cleanup_intents SET state=$3,state_version=$4,attempts=$5,next_attempt_at=$6,last_error_code=$7,updated_at=$8,confirmed_at=$9,quarantined_at=$10 WHERE tenant_id=$1 AND cleanup_intent_id=$2 AND state='PENDING' AND state_version=$11`, r.tenantID, value.ID(), value.State(), value.Version(), value.Attempts(), value.NextAttemptAt(), nullString(value.LastErrorCode()), value.UpdatedAt(), nullableTime(confirmed, hasConfirmed), nullableTime(quarantined, hasQuarantined), expectedVersion)
	return affected("update cleanup intent", result, err)
}

const cleanupSelect = `SELECT cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,state_version,attempts,next_attempt_at,last_error_code,created_at,updated_at,confirmed_at,quarantined_at FROM cleanup_intents`

func scanCleanup(row rowScanner, tenantID primitives.ID) (*cleanup.Intent, error) {
	var id, ownerID, operationID primitives.ID
	var target cleanup.Target
	var state string
	var version, attempts uint64
	var next, created, updated time.Time
	var lastError sql.NullString
	var confirmed, quarantined sql.NullTime
	if err := row.Scan(&id, &target.OwnerType, &ownerID, &target.TargetType, &target.ProviderKind, &target.ExternalReference, &operationID, &target.RequestDigest, &target.OwnershipProofDigest, &target.Orphan, &state, &version, &attempts, &next, &lastError, &created, &updated, &confirmed, &quarantined); err != nil {
		return nil, wrap("scan cleanup intent", err)
	}
	target.OwnerID, target.OperationID = ownerID, operationID
	return cleanup.Restore(tenantID, id, target, cleanup.State(state), version, attempts, next, lastError.String, created, updated, timePtr(confirmed), timePtr(quarantined))
}

func wrap(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, persistence.ErrNotFound)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "23505" || postgresError.Code == "40001" || postgresError.Code == "40P01") {
		return fmt.Errorf("%s: %w", operation, persistence.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
func affected(operation string, result sql.Result, err error) error {
	if err != nil {
		return wrap(operation, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return wrap(operation, err)
	}
	if n != 1 {
		return fmt.Errorf("%s: %w", operation, persistence.ErrConflict)
	}
	return nil
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullID(v primitives.ID) any {
	if v == "" {
		return nil
	}
	return v
}
func nullableTime(v time.Time, ok bool) any {
	if !ok {
		return nil
	}
	return v
}
func timePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	x := v.Time
	return &x
}
func nullableID(v primitives.ID, ok bool) any {
	if !ok {
		return nil
	}
	return v
}
func executionTerminal(v *execution.Execution) (any, any, any) {
	result, ok := v.TerminalResult()
	if !ok {
		return nil, nil, nil
	}
	at, _ := v.TerminalAt()
	return result.Reference, result.Digest, at
}
func attemptTerminal(v *attempt.Attempt) (any, any, any) {
	result, ok := v.TerminalResult()
	if !ok {
		return nil, nil, nil
	}
	at, _ := v.TerminalAt()
	return result.Reference, result.Digest, at
}
