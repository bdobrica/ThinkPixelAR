package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
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
