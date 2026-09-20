package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"github.com/jackc/pgx/v5/pgconn"
)

// SandboxBindings commits each reservation before returning to the provider.
// It shares AR's authoritative tables; it never accesses another component's DB.
type SandboxBindings struct{ db *sql.DB }

var _ sandbox.BindingStore = (*SandboxBindings)(nil)
var _ sandbox.LifecycleStore = (*SandboxBindings)(nil)

func NewSandboxBindings(db *sql.DB) (*SandboxBindings, error) {
	if db == nil {
		return nil, sandbox.ErrInvalid
	}
	return &SandboxBindings{db: db}, nil
}
func (s *SandboxBindings) transaction(ctx context.Context, tenant primitives.ID, work func(*sql.Tx) error) error {
	if _, err := primitives.ParseID(string(tenant)); err != nil {
		return sandbox.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return bindingError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return bindingError(err)
	}
	if err = work(tx); err != nil {
		return bindingError(err)
	}
	return bindingError(tx.Commit())
}
func readSandboxBinding(ctx context.Context, tx *sql.Tx, tenant, id primitives.ID, lock bool) (sandbox.Binding, uint64, error) {
	q := `SELECT r.canonical_request,COALESCE(b.provider_reference,''),r.operation_revision FROM sandbox_binding_requests r JOIN sandbox_bindings b USING (tenant_id,sandbox_binding_id) WHERE r.tenant_id=$1 AND r.sandbox_binding_id=$2`
	if lock {
		q += ` FOR UPDATE OF b,r`
	}
	var b sandbox.Binding
	var raw []byte
	var revision uint64
	err := tx.QueryRowContext(ctx, q, tenant, id).Scan(&raw, &b.ProviderReference, &revision)
	if err != nil {
		return b, 0, err
	}
	if json.Unmarshal(raw, &b.Request) != nil || b.Request.Scope.TenantID != tenant || b.Request.Scope.SandboxID != id {
		return b, 0, sandbox.ErrIntegrity
	}
	digest, err := sandbox.RequestDigest(b.Request)
	if err != nil || digest != b.Request.Operation.Digest {
		return b, 0, sandbox.ErrIntegrity
	}
	return b, revision, nil
}
func (s *SandboxBindings) Get(ctx context.Context, tenant, id primitives.ID) (sandbox.Binding, error) {
	var result sandbox.Binding
	err := s.transaction(ctx, tenant, func(tx *sql.Tx) error {
		var e error
		result, _, e = readSandboxBinding(ctx, tx, tenant, id, false)
		return e
	})
	return result, err
}

// lockFence serializes with Session admission, Execution cancellation and Attempt
// replacement. A false result permits only independently authorized cleanup.
func lockSandboxFence(ctx context.Context, tx *sql.Tx, r sandbox.AcquireRequest) (bool, error) {
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT s.state='ACTIVE' AND s.current_execution_id=e.execution_id AND s.execution_generation=$5 AND e.state IN ('MATERIALIZING','RUNNING') AND e.deadline>CURRENT_TIMESTAMP AND e.deadline >= $7 AND a.is_current AND a.attempt_no=$6 AND a.execution_generation=$5 AND a.state IN ('PENDING','ACQUIRING','STARTING','RUNNING') FROM sessions s JOIN executions e ON e.tenant_id=s.tenant_id AND e.session_id=s.session_id JOIN attempts a ON a.tenant_id=e.tenant_id AND a.execution_id=e.execution_id WHERE s.tenant_id=$1 AND s.session_id=$2 AND e.execution_id=$3 AND a.attempt_id=$4 FOR UPDATE OF s,e,a`, r.Scope.TenantID, r.Scope.SessionID, r.Scope.ExecutionID, r.Scope.AttemptID, r.Scope.Generation, r.Scope.AttemptOrdinal, r.Deadline).Scan(&current)
	return current, err
}
func (s *SandboxBindings) Reserve(ctx context.Context, r sandbox.AcquireRequest) (sandbox.Binding, error) {
	var result sandbox.Binding
	if _, err := primitives.ParseID(r.Operation.ID); err != nil {
		return result, sandbox.ErrInvalid
	}
	for _, id := range []primitives.ID{r.Scope.SessionID, r.Scope.ExecutionID, r.Scope.AttemptID, r.Scope.SandboxID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return result, sandbox.ErrInvalid
		}
	}
	digest, err := sandbox.RequestDigest(r)
	if err != nil || digest != r.Operation.Digest || r.Profile.ValidateConstraints() != nil || !r.Deadline.After(time.Now()) {
		return result, sandbox.ErrInvalid
	}
	raw, err := sandbox.CanonicalRequest(r)
	if err != nil {
		return result, err
	}
	err = s.transaction(ctx, r.Scope.TenantID, func(tx *sql.Tx) error {
		if e := lockSandboxOperation(ctx, tx, r.Scope.TenantID, r.Operation.ID); e != nil {
			return e
		}
		var used bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_operations WHERE tenant_id=$1 AND operation_id=$2)`, r.Scope.TenantID, r.Operation.ID).Scan(&used); e != nil {
			return e
		}
		if used {
			return sandbox.ErrConflict
		}
		current, e := lockSandboxFence(ctx, tx, r)
		if e != nil {
			return e
		}
		if !current {
			return sandbox.ErrConflict
		}
		// An Execution's already persisted resolution is authoritative for this
		// reservation; a supplied profile or runtime cannot substitute for it.
		var profileRaw, runtimeRaw []byte
		var pd, id string
		e = tx.QueryRowContext(ctx, `SELECT p.canonical_resolution,p.resolution_digest,p.implementation_digest,s.runtime_spec FROM runtime_profile_resolution_snapshots p JOIN executions e USING(tenant_id,execution_id) JOIN sessions s ON s.tenant_id=e.tenant_id AND s.session_id=e.session_id WHERE p.tenant_id=$1 AND p.execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID).Scan(&profileRaw, &pd, &id, &runtimeRaw)
		if e != nil {
			return e
		}
		var profile runtimeprofile.Profile
		var runtime struct {
			Image struct {
				Reference string `json:"reference"`
			} `json:"image"`
			Entrypoint struct {
				Command          []string `json:"command"`
				WorkingDirectory string   `json:"working_directory"`
				ShutdownGrace    int64    `json:"shutdown_grace_seconds"`
			} `json:"entrypoint"`
			Platform struct {
				Architectures []string `json:"architectures"`
			} `json:"platform"`
		}
		if pd != r.ProfileDigest || id != r.ImplementationDigest || sandbox.Digest(profileRaw) != pd || json.Unmarshal(profileRaw, &profile) != nil || !reflect.DeepEqual(profile, r.Profile) || json.Unmarshal(runtimeRaw, &runtime) != nil || runtime.Image.Reference != r.Runtime.Image || !reflect.DeepEqual(runtime.Entrypoint.Command, r.Runtime.Entrypoint) || !slices.Contains(runtime.Platform.Architectures, r.Runtime.Architecture) {
			return sandbox.ErrIntegrity
		}
		if runtime.Entrypoint.WorkingDirectory != "" && runtime.Entrypoint.WorkingDirectory != "/workspace" || runtime.Entrypoint.ShutdownGrace > r.Profile.Lifecycle.TerminationGraceSeconds {
			return sandbox.ErrUnsupported
		}
		previous, revision, e := readSandboxBinding(ctx, tx, r.Scope.TenantID, r.Scope.SandboxID, true)
		if e == nil {
			saved, _ := sandbox.CanonicalRequest(previous.Request)
			if !bytes.Equal(saved, raw) {
				return sandbox.ErrConflict
			}
			if revision > 0 {
				var kind string
				if e = tx.QueryRowContext(ctx, `SELECT kind FROM sandbox_operations WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND revision=$3`, r.Scope.TenantID, r.Scope.SandboxID, revision).Scan(&kind); e != nil {
					return e
				}
				if kind == "release" {
					return sandbox.ErrConflict
				}
			}
			result = previous
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO sandbox_bindings(tenant_id,sandbox_binding_id,session_id,execution_id,execution_generation,attempt_id,attempt_no,provider_kind,resolution_digest,acquire_operation_id,acquire_request_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, r.Scope.TenantID, r.Scope.SandboxID, r.Scope.SessionID, r.Scope.ExecutionID, r.Scope.Generation, r.Scope.AttemptID, r.Scope.AttemptOrdinal, r.Profile.Implementation.ProviderKind, r.ProfileDigest, r.Operation.ID, r.Operation.Digest)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO sandbox_binding_requests(tenant_id,sandbox_binding_id,acquire_operation_id,canonical_request) VALUES($1,$2,$3,$4)`, r.Scope.TenantID, r.Scope.SandboxID, r.Operation.ID, raw)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE attempts SET sandbox_binding_reference=$3,state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID, r.Scope.SandboxID)
		if e == nil {
			result = sandbox.Binding{Request: r}
		}
		return e
	})
	return result, err
}
func (s *SandboxBindings) BindReference(ctx context.Context, tenant, id primitives.ID, reference string) error {
	if len(reference) == 0 || len(reference) > 2048 {
		return sandbox.ErrInvalid
	}
	return s.transaction(ctx, tenant, func(tx *sql.Tx) error {
		b, _, err := readSandboxBinding(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}
		if b.ProviderReference != "" {
			if b.ProviderReference != reference {
				return sandbox.ErrIntegrity
			}
			return nil
		}
		// Recording an ambiguous external result remains valid after fencing: it is
		// ownership evidence for cleanup, not permission to restart execution.
		_, err = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET provider_reference=$3,state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, id, reference)
		return err
	})
}
func (s *SandboxBindings) BeginOperation(ctx context.Context, tenant, id primitives.ID, kind string, op sandbox.Operation) (uint64, error) {
	if _, err := primitives.ParseID(op.ID); err != nil || (kind != "suspend" && kind != "resume" && kind != "release") || op.Digest != sandbox.LifecycleDigest(tenant, id, kind, op.ID) {
		return 0, sandbox.ErrInvalid
	}
	var revision uint64
	err := s.transaction(ctx, tenant, func(tx *sql.Tx) error {
		if e := lockSandboxOperation(ctx, tx, tenant, op.ID); e != nil {
			return e
		}
		var used bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_binding_requests WHERE tenant_id=$1 AND acquire_operation_id=$2)`, tenant, op.ID).Scan(&used); e != nil {
			return e
		}
		if used {
			return sandbox.ErrConflict
		}
		b, _, e := readSandboxBinding(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		current, e := lockSandboxFence(ctx, tx, b.Request)
		if e != nil {
			return e
		}
		b, revision, e = readSandboxBinding(ctx, tx, tenant, id, true)
		if e != nil {
			return e
		}
		if !current {
			if kind != "release" {
				return sandbox.ErrConflict
			}
			var cleanup bool
			e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cleanup_intents WHERE tenant_id=$1 AND owner_type='sandbox-binding' AND owner_id=$2 AND target_type='sandbox' AND provider_kind=$7 AND external_reference=$3 AND cleanup_operation_id=$4 AND request_digest=$5 AND ownership_proof_digest=$6 AND state IN ('PENDING','CONFIRMED'))`, tenant, id, b.ProviderReference, op.ID, op.Digest, b.Request.Operation.Digest, b.Request.Profile.Implementation.ProviderKind).Scan(&cleanup)
			if e != nil {
				return e
			}
			if !cleanup {
				return sandbox.ErrConflict
			}
		}
		var previousID, previousKind, previousDigest string
		var previousRevision uint64
		e = tx.QueryRowContext(ctx, `SELECT sandbox_binding_id,kind,request_digest,revision FROM sandbox_operations WHERE tenant_id=$1 AND operation_id=$2`, tenant, op.ID).Scan(&previousID, &previousKind, &previousDigest, &previousRevision)
		if e == nil {
			if previousID != string(id) || previousKind != kind || previousDigest != op.Digest || previousRevision != revision {
				return sandbox.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if revision > 0 {
			var last string
			if e = tx.QueryRowContext(ctx, `SELECT kind FROM sandbox_operations WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND revision=$3`, tenant, id, revision).Scan(&last); e != nil {
				return e
			}
			if last == "release" {
				return sandbox.ErrConflict
			}
		}
		revision++
		_, e = tx.ExecContext(ctx, `INSERT INTO sandbox_operations(tenant_id,sandbox_binding_id,operation_id,kind,request_digest,revision) VALUES($1,$2,$3,$4,$5,$6)`, tenant, id, op.ID, kind, op.Digest, revision)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE sandbox_binding_requests SET operation_revision=$3 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, id, revision)
		if e != nil {
			return e
		}
		// kind is a closed vocabulary above, never caller-controlled SQL syntax.
		_, e = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET `+kind+`_operation_id=COALESCE(`+kind+`_operation_id,$3::uuid),`+kind+`_request_digest=COALESCE(`+kind+`_request_digest,$4),state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, id, op.ID, op.Digest)
		return e
	})
	return revision, err
}
func bindingError(err error) error {
	if err == nil {
		return nil
	}
	for _, stable := range []error{sandbox.ErrInvalid, sandbox.ErrUnsupported, sandbox.ErrConflict, sandbox.ErrNotFound, sandbox.ErrUnavailable, sandbox.ErrTimeout, sandbox.ErrPermission, sandbox.ErrIntegrity} {
		if errors.Is(err, stable) {
			return stable
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.ErrNotFound
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return sandbox.ErrTimeout
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505", "23503", "23514", "40001", "40P01":
			return sandbox.ErrConflict
		case "42501":
			return sandbox.ErrPermission
		}
	}
	return sandbox.ErrUnavailable
}

func lockSandboxOperation(ctx context.Context, tx *sql.Tx, tenant primitives.ID, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(tenant)+"/sandbox-operation/"+id)
	return err
}
