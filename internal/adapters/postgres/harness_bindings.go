package postgres

import (
	"context"
	"database/sql"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type HarnessBindings struct{ bindings *SandboxBindings }

var _ harness.BindingReader = (*HarnessBindings)(nil)

func NewHarnessBindings(db *sql.DB) (*HarnessBindings, error) {
	b, err := NewSandboxBindings(db)
	if err != nil {
		return nil, err
	}
	return &HarnessBindings{bindings: b}, nil
}

// Load reads the same immutable association even after the process disappears.
// Callers must separately authorize continuation/checkpoint access for the tenant.
func (s *HarnessBindings) Load(ctx context.Context, tenant, id primitives.ID) (harness.HarnessHandle, error) {
	var h harness.HarnessHandle
	err := s.bindings.transaction(ctx, tenant, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT harness_binding_id,process_reference,tenant_id,session_id,execution_id,attempt_id,sandbox_binding_id,execution_generation,attempt_no,adapter_kind,adapter_build_digest,negotiation_digest,vendor_session_reference FROM harness_bindings WHERE tenant_id=$1 AND harness_binding_id=$2`, tenant, id).Scan(&h.ID, &h.ProcessInstanceID, &h.Fence.TenantID, &h.Fence.SessionID, &h.Fence.ExecutionID, &h.Fence.AttemptID, &h.Fence.SandboxBindingID, &h.Fence.Generation, &h.Fence.AttemptOrdinal, &h.AdapterKind, &h.AdapterBuildDigest, &h.NegotiationDigest, &h.VendorSessionReference)
	})
	if err != nil {
		return harness.HarnessHandle{}, err
	}
	return h, nil
}

// persistThread runs inside frame authorization's transaction, after the current
// Session/Execution/Attempt, live grant, epoch and dispatch claim are locked.
// Only the vendor/process IDs are observations; all ownership/build pins come
// from trusted materialization. No Session or Execution state is advanced.
func persistThread(ctx context.Context, tx *sql.Tx, b sandbox.Binding, m AgentdMaterialization, f *agentdv1.Envelope) error {
	value, err := control.ThreadObservation(f)
	if err != nil {
		return ErrAgentdPolicy
	}
	s := b.Request.Scope
	spec := runtimebinding.HarnessSpecification{AdapterKind: codex.Kind, AdapterVersion: codex.Version, AdapterBuildDigest: m.Config.AdapterDigest, NegotiationDigest: m.HarnessNegotiationDigest, ProtocolName: "app-server", ProtocolVersion: codex.Version, ProcessReference: string(value.ProcessID), VendorSessionReference: value.ThreadID}
	if _, err := runtimebinding.NewHarness(s.TenantID, primitives.ID(m.HarnessHandle), s.SessionID, s.ExecutionID, s.AttemptID, s.SandboxID, s.Generation, s.AttemptOrdinal, spec, runtimebinding.OperationIdentity{ID: primitives.ID(f.OperationId), RequestDigest: f.RequestDigest}, time.Now()); err != nil {
		return ErrAgentdPolicy
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO harness_bindings(tenant_id,harness_binding_id,session_id,execution_id,execution_generation,attempt_id,attempt_no,sandbox_binding_id,adapter_kind,adapter_version,adapter_build_digest,negotiation_digest,protocol_name,protocol_version,process_reference,vendor_session_reference,start_operation_id,start_request_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) ON CONFLICT(tenant_id,harness_binding_id) DO NOTHING`, s.TenantID, m.HarnessHandle, s.SessionID, s.ExecutionID, s.Generation, s.AttemptID, s.AttemptOrdinal, s.SandboxID, spec.AdapterKind, spec.AdapterVersion, spec.AdapterBuildDigest, spec.NegotiationDigest, spec.ProtocolName, spec.ProtocolVersion, spec.ProcessReference, spec.VendorSessionReference, f.OperationId, f.RequestDigest)
	if err != nil {
		return err
	}
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT session_id=$3 AND execution_id=$4 AND execution_generation=$5 AND attempt_id=$6 AND attempt_no=$7 AND sandbox_binding_id=$8 AND adapter_kind=$9 AND adapter_version=$10 AND adapter_build_digest=$11 AND negotiation_digest=$12 AND protocol_name=$13 AND protocol_version=$14 AND process_reference=$15 AND vendor_session_reference=$16 AND start_operation_id=$17 AND start_request_digest=$18 FROM harness_bindings WHERE tenant_id=$1 AND harness_binding_id=$2`, s.TenantID, m.HarnessHandle, s.SessionID, s.ExecutionID, s.Generation, s.AttemptID, s.AttemptOrdinal, s.SandboxID, spec.AdapterKind, spec.AdapterVersion, spec.AdapterBuildDigest, spec.NegotiationDigest, spec.ProtocolName, spec.ProtocolVersion, spec.ProcessReference, spec.VendorSessionReference, f.OperationId, f.RequestDigest).Scan(&same)
	if err != nil || !same {
		return ErrAgentdPolicy
	}
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(harness_binding_reference::text,'') FROM attempts WHERE tenant_id=$1 AND attempt_id=$2`, s.TenantID, s.AttemptID).Scan(&previous); err != nil {
		return err
	}
	if previous != "" {
		if previous != m.HarnessHandle {
			return ErrAgentdPolicy
		}
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE attempts SET harness_binding_reference=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND attempt_id=$2`, s.TenantID, s.AttemptID, m.HarnessHandle)
	return err
}
