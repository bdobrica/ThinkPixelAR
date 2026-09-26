package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

var ErrAgentdPolicy = errors.New("agentd policy rejected")

// AgentdMaterialization is registered only by trusted AR materialization code,
// after Execution admission. It contains no credentials and cannot issue grants.
type AgentdMaterialization struct {
	Config                                                   agentd.Config
	Challenge                                                []byte
	GrantDigest, AuthorityReference, Revision, RequestDigest string
	HarnessHandle                                            string
	HarnessNegotiationDigest                                 string
	ExecuteOperationID, ExecuteDigest                        string // Trusted input identity/digest; no prompt content.
	Deadline, BootstrapDeadline                              time.Time
}

// AgentdPolicy validates already admitted local Executions; it is not the
// LocalAuthority grant issuer. It never authorizes ThinkPixelAG mode.
type AgentdPolicy struct {
	credentials      *AgentdCredentials
	revision, issuer string
}

var _ transport.AdmissionPolicy = (*AgentdPolicy)(nil)
var _ transport.FramePolicy = (*AgentdPolicy)(nil)
var _ transport.CommandOutcomes = (*AgentdPolicy)(nil)

func NewAgentdLocalPolicy(db *sql.DB, mode, revision, issuer string) (*AgentdPolicy, error) {
	if mode != "local" || !credentialDigest(revision) || issuer != "thinkpixelar/local" {
		return nil, ErrAgentdPolicy
	}
	c, err := NewAgentdCredentials(db)
	if err != nil {
		return nil, ErrAgentdPolicy
	}
	return &AgentdPolicy{c, revision, issuer}, nil
}
func policyIdentity(b *agentdv1.Binding) transport.Identity {
	if b == nil {
		return transport.Identity{}
	}
	return transport.Identity{TenantID: primitives.ID(b.TenantId), SandboxID: primitives.ID(b.SandboxBindingId), AttemptID: primitives.ID(b.AttemptId)}
}
func (p *AgentdPolicy) validate(ctx context.Context, tx *sql.Tx, b sandbox.Binding, m AgentdMaterialization) error {
	c := m.Config
	r := b.Request
	s := r.Scope
	// New runnable materializations carry the same immutable local cutoff as AR.
	// Legacy snapshots without it remain readable, but the binary rejects them.
	if c.ControlDeadlineUnixMS != 0 && (c.ControlDeadlineUnixMS != m.Deadline.UnixMilli() || c.Harness.StopGraceMS+c.Harness.KillWaitMS >= 5000 || int64(c.Harness.StartTimeoutMS+3*c.Harness.StopGraceMS+3*c.Harness.KillWaitMS)+5000 >= r.Profile.Lifecycle.TerminationGraceSeconds*1000) {
		return ErrAgentdPolicy
	}
	if c.Validate() != nil || c.Binding == nil || len(m.Challenge) != 32 || !credentialDigest(m.GrantDigest) || m.Revision != p.revision || m.RequestDigest != r.Operation.Digest || !m.Deadline.After(time.Now()) || m.Deadline.After(r.Deadline) || m.BootstrapDeadline.After(m.Deadline) || m.BootstrapDeadline.IsZero() {
		return ErrAgentdPolicy
	}
	if !slices.Contains(c.RequiredCapabilities, control.Capability) || !slices.Contains(c.RequiredCapabilities, "rotation.v1") {
		return ErrAgentdPolicy
	}
	if slices.Contains(c.Capabilities, control.ThreadCapability) && (c.AdapterKind != codex.Kind || !slices.Contains(c.RequiredCapabilities, control.ThreadCapability) || !slices.Equal(c.Harness.Argv, codex.Command()) || !credentialDigest(m.HarnessNegotiationDigest)) {
		return ErrAgentdPolicy
	}
	if slices.Contains(c.Capabilities, control.TurnCapability) {
		if !slices.Contains(c.RequiredCapabilities, control.TurnCapability) || !slices.Contains(c.RequiredCapabilities, control.ThreadCapability) || !credentialDigest(m.ExecuteDigest) {
			return ErrAgentdPolicy
		}
		if _, err := primitives.ParseID(m.ExecuteOperationID); err != nil {
			return ErrAgentdPolicy
		}
	}
	if _, err := primitives.ParseID(m.HarnessHandle); err != nil {
		return ErrAgentdPolicy
	}
	want := &agentdv1.Binding{TenantId: string(s.TenantID), SessionId: string(s.SessionID), ExecutionId: string(s.ExecutionID), AttemptId: string(s.AttemptID), SandboxBindingId: string(s.SandboxID), SessionGeneration: s.Generation}
	if !proto.Equal(c.Binding, want) {
		return ErrAgentdPolicy
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT authority_mode='LOCAL' AND authority_namespace=$3 AND authority_reference=$4 AND grant_digest=$5 AND created_at<=clock_timestamp() AND deadline>clock_timestamp() AND $6::timestamptz<=deadline FROM executions WHERE tenant_id=$1 AND execution_id=$2`, s.TenantID, s.ExecutionID, p.issuer, m.AuthorityReference, m.GrantDigest, m.Deadline).Scan(&valid)
	if err != nil || !valid {
		return ErrAgentdPolicy
	}
	return nil
}
func (p *AgentdPolicy) Register(ctx context.Context, m AgentdMaterialization) error {
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 98304 {
		return ErrAgentdPolicy
	}
	err = p.credentials.transaction(ctx, policyIdentity(m.Config.Binding), func(tx *sql.Tx, b sandbox.Binding) error {
		if err := p.validate(ctx, tx, b, m); err != nil {
			return err
		}
		if !m.BootstrapDeadline.After(time.Now()) {
			return ErrAgentdPolicy
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO agentd_admission(tenant_id,sandbox_binding_id,snapshot) VALUES($1,$2,$3)`, b.Request.Scope.TenantID, b.Request.Scope.SandboxID, raw)
		return err
	})
	if err != nil {
		return ErrAgentdPolicy
	}
	return nil
}
func (p *AgentdPolicy) load(ctx context.Context, tx *sql.Tx, b sandbox.Binding) (AgentdMaterialization, error) {
	var m AgentdMaterialization
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT snapshot FROM agentd_admission WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND NOT revoked FOR UPDATE`, b.Request.Scope.TenantID, b.Request.Scope.SandboxID).Scan(&raw)
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return m, ErrAgentdPolicy
	}
	return m, p.validate(ctx, tx, b, m)
}
func (p *AgentdPolicy) AuthorizeTransport(ctx context.Context, i sandbox.ComputeIntent, purpose transport.Purpose) (transport.Authorization, error) {
	var a transport.Authorization
	if !i.Current || i.Desired != sandbox.ComputeRunning {
		return a, ErrAgentdPolicy
	}
	// Immutable Secret projection has no safe same-Pod recovery delivery.
	switch purpose {
	case transport.IssueBootstrap, transport.AcceptStream, transport.DeliverFrame, transport.RenewIdentity:
	default:
		return a, ErrAgentdPolicy
	}
	s := i.Binding.Request.Scope
	err := p.credentials.transaction(ctx, transport.Identity{TenantID: s.TenantID, SandboxID: s.SandboxID, AttemptID: s.AttemptID}, func(tx *sql.Tx, b sandbox.Binding) error {
		if b.Request.Operation.Digest != i.Binding.Request.Operation.Digest {
			return ErrAgentdPolicy
		}
		m, err := p.load(ctx, tx, b)
		if err != nil {
			return err
		}
		c := m.Config
		if purpose == transport.IssueBootstrap && !m.BootstrapDeadline.After(time.Now()) {
			return ErrAgentdPolicy
		}
		a = transport.Authorization{Deadline: m.Deadline, BootstrapDeadline: m.BootstrapDeadline, Expected: transport.Expectations{Binding: c.Binding, Challenge: m.Challenge, BuildDigest: c.BuildDigest, AdapterKind: c.AdapterKind, AdapterDigest: c.AdapterDigest, SupportedCapabilities: c.Capabilities, RequiredCapabilities: c.RequiredCapabilities, Limits: c.Limits}}
		return nil
	})
	if err != nil {
		return transport.Authorization{}, ErrAgentdPolicy
	}
	return a, nil
}

// Revoke is irreversible and needs no active Execution or live grant.
func (p *AgentdPolicy) Revoke(ctx context.Context, tenant, sandboxID primitives.ID) error {
	err := p.credentials.bindings.transaction(ctx, tenant, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, `UPDATE agentd_admission SET revoked=true WHERE tenant_id=$1 AND sandbox_binding_id=$2`, tenant, sandboxID)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if n != 1 {
			return ErrAgentdPolicy
		}
		return e
	})
	if err != nil {
		return ErrAgentdPolicy
	}
	return nil
}

// AuthorizeFrame commits dispatch intent before Send and records only correlated
// transport outcomes. A PENDING row is ambiguous after a crash, never retryable.
func (p *AgentdPolicy) AuthorizeFrame(ctx context.Context, i sandbox.ComputeIntent, c transport.Connection, f *agentdv1.Envelope) error {
	if f == nil || f.Sequence == 0 || f.Sequence > math.MaxInt64 || f.ConnectionId != string(c.ID) || f.ConnectionEpoch != c.Epoch || !i.Current || i.Desired != sandbox.ComputeRunning {
		return ErrAgentdPolicy
	}
	if _, err := primitives.ParseID(f.MessageId); err != nil {
		return ErrAgentdPolicy
	}
	s := i.Binding.Request.Scope
	err := p.credentials.transaction(ctx, transport.Identity{TenantID: s.TenantID, SandboxID: s.SandboxID, AttemptID: s.AttemptID}, func(tx *sql.Tx, b sandbox.Binding) error {
		m, err := p.load(ctx, tx, b)
		if err != nil {
			return err
		}
		if b.Request.Operation.Digest != i.Binding.Request.Operation.Digest || !proto.Equal(f.Binding, m.Config.Binding) || f.DeadlineUnixMs > m.Deadline.UnixMilli() {
			return ErrAgentdPolicy
		}
		var current bool
		err = tx.QueryRowContext(ctx, `SELECT connection_id=$3 AND connection_epoch=$4 AND connection_deadline>clock_timestamp() FROM agentd_credential_state WHERE tenant_id=$1 AND sandbox_binding_id=$2 FOR UPDATE`, s.TenantID, s.SandboxID, c.ID, c.Epoch).Scan(&current)
		if err != nil || !current {
			return ErrAgentdPolicy
		}
		if h := f.GetHeartbeat(); h != nil {
			var sent uint64
			err = tx.QueryRowContext(ctx, `SELECT sequence FROM agentd_frame_sequences WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND connection_id=$3 AND direction='AR'`, s.TenantID, s.SandboxID, c.ID).Scan(&sent)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if h.LastAcceptedSequence > sent || h.LastProducedSequence >= f.Sequence {
				return ErrAgentdPolicy
			}
			if h.ActiveOperationId != "" {
				var active bool
				err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agentd_commands WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND operation_id=$3 AND connection_id=$4 AND outcome='PENDING')`, s.TenantID, s.SandboxID, h.ActiveOperationId, c.ID).Scan(&active)
				if err != nil || !active {
					return ErrAgentdPolicy
				}
			}
		}
		direction, err := policyFrame(f, m)
		if err != nil {
			return err
		}
		if ack := f.GetAcknowledgement(); ack != nil && f.OperationId == "" {
			var last uint64
			if err = tx.QueryRowContext(ctx, `SELECT sequence FROM agentd_frame_sequences WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND connection_id=$3 AND direction='AGENTD'`, s.TenantID, s.SandboxID, c.ID).Scan(&last); err != nil || ack.AcceptedSequence > last {
				return ErrAgentdPolicy
			}
		}
		if err = policySequence(ctx, tx, s, f, direction); err != nil {
			return err
		}
		if f.GetCommand() != nil {
			if f.GetCommand().Kind == agentdv1.Command_EXECUTE {
				var ready bool
				err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM harness_bindings h JOIN agentd_commands c ON c.tenant_id=h.tenant_id AND c.operation_id=h.start_operation_id WHERE h.tenant_id=$1 AND h.harness_binding_id=$2 AND h.attempt_id=$3 AND h.execution_id=$4 AND c.outcome='ACKNOWLEDGED')`, s.TenantID, m.HarnessHandle, s.AttemptID, s.ExecutionID).Scan(&ready)
				if err != nil || !ready {
					return ErrAgentdPolicy
				}
			}
			if slices.Contains(m.Config.RequiredCapabilities, control.ThreadCapability) {
				if f.GetCommand().Kind == agentdv1.Command_RESTART {
					return ErrAgentdPolicy
				}
				if f.GetCommand().Kind == agentdv1.Command_START {
					var exists bool
					if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM harness_bindings WHERE tenant_id=$1 AND session_id=$2)`, s.TenantID, s.SessionID).Scan(&exists); err != nil || exists {
						return ErrAgentdPolicy
					}
				}
			}
			var count, pending int
			if err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome<>'ACKNOWLEDGED') FROM agentd_commands WHERE tenant_id=$1 AND sandbox_binding_id=$2`, s.TenantID, s.SandboxID).Scan(&count, &pending); err != nil {
				return err
			}
			if count >= 128 || pending != 0 {
				return ErrAgentdPolicy
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO agentd_commands(tenant_id,sandbox_binding_id,operation_id,request_digest,connection_id,connection_epoch,message_id,sequence) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.TenantID, s.SandboxID, f.OperationId, f.RequestDigest, c.ID, c.Epoch, f.MessageId, f.Sequence)
			return err
		}
		if f.OperationId != "" {
			var digest, cid, mid, outcome string
			var epoch, sequence uint64
			err = tx.QueryRowContext(ctx, `SELECT request_digest,connection_id,message_id,connection_epoch,sequence,outcome FROM agentd_commands WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND operation_id=$3 FOR UPDATE`, s.TenantID, s.SandboxID, f.OperationId).Scan(&digest, &cid, &mid, &epoch, &sequence, &outcome)
			if err != nil || digest != f.RequestDigest || cid != f.ConnectionId || epoch != f.ConnectionEpoch {
				return ErrAgentdPolicy
			}
			if f.GetObservation() != nil {
				if outcome != "PENDING" && outcome != "ACKNOWLEDGED" {
					return ErrAgentdPolicy
				}
				return persistThread(ctx, tx, b, m, f)
			}
			next := "UNKNOWN"
			if ack := f.GetAcknowledgement(); ack != nil {
				if ack.MessageId != mid || ack.RequestDigest != digest || ack.AcceptedSequence != sequence || ack.EventCredit != 0 {
					return ErrAgentdPolicy
				}
				if slices.Contains(m.Config.RequiredCapabilities, control.ThreadCapability) && digest == control.Digest(agentdv1.Command_START, agentd.ConfigurationDigest(m.Config), m.HarnessHandle) {
					var exists bool
					if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM harness_bindings WHERE tenant_id=$1 AND harness_binding_id=$2 AND start_operation_id=$3 AND start_request_digest=$4)`, s.TenantID, m.HarnessHandle, f.OperationId, digest).Scan(&exists); err != nil || !exists {
						return ErrAgentdPolicy
					}
				}
				next = "ACKNOWLEDGED"
			}
			if outcome != "PENDING" && outcome != next {
				return ErrAgentdPolicy
			}
			_, err = tx.ExecContext(ctx, `UPDATE agentd_commands SET outcome=$4 WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND operation_id=$3`, s.TenantID, s.SandboxID, f.OperationId, next)
			return err
		}
		return nil
	})
	if err != nil {
		return ErrAgentdPolicy
	}
	return nil
}
func policyFrame(f *agentdv1.Envelope, m AgentdMaterialization) (string, error) {
	if !protocol.KnownFields(f) || proto.Size(f) > int(m.Config.Limits.FrameBytes) {
		return "", ErrAgentdPolicy
	}
	if !proto.Equal(f.Binding, m.Config.Binding) || f.Major != m.Config.Protocol.Major || f.Minor > m.Config.Protocol.MaximumMinor || f.Minor < m.Config.Protocol.MinimumMinor {
		return "", ErrAgentdPolicy
	}
	if f.HarnessHandle != "" && f.HarnessHandle != m.HarnessHandle {
		return "", ErrAgentdPolicy
	}
	if f.GetCommand() != nil {
		if f.GetCommand().Kind == agentdv1.Command_EXECUTE && (!slices.Contains(m.Config.RequiredCapabilities, control.TurnCapability) || f.OperationId != m.ExecuteOperationID || f.RequestDigest != m.ExecuteDigest) {
			return "", ErrAgentdPolicy
		}
		raw, _ := json.Marshal(m.Config)
		if f.HarnessHandle != m.HarnessHandle || control.Command(f, sandbox.Digest(raw)) != nil {
			return "", ErrAgentdPolicy
		}
		return "AR", nil
	}
	if o := f.GetObservation(); o != nil && o.PayloadSchema == control.ThreadCapability {
		if !slices.Contains(m.Config.RequiredCapabilities, control.ThreadCapability) || f.HarnessHandle != m.HarnessHandle || f.OperationId == "" || f.RequestDigest != control.Digest(agentdv1.Command_START, agentd.ConfigurationDigest(m.Config), m.HarnessHandle) {
			return "", ErrAgentdPolicy
		}
		if _, err := control.ThreadObservation(f); err != nil {
			return "", ErrAgentdPolicy
		}
		return "AGENTD", nil
	}
	if f.OperationId != "" && f.GetAcknowledgement() == nil && f.GetFailure() == nil {
		return "", ErrAgentdPolicy
	}
	if ack := f.GetAcknowledgement(); ack != nil && f.OperationId == "" {
		if _, err := primitives.ParseID(ack.MessageId); err != nil || f.RequestDigest != "" || ack.RequestDigest != "" || ack.AcceptedSequence == 0 || ack.EventCredit != 0 {
			return "", ErrAgentdPolicy
		}
		return "AR", nil
	}
	if f.GetAcknowledgement() != nil || f.GetFailure() != nil {
		if f.OperationId == "" || f.HarnessHandle != m.HarnessHandle {
			return "", ErrAgentdPolicy
		}
		if failure := f.GetFailure(); failure != nil && failure.Code != agentdv1.Failure_OUTCOME_UNKNOWN {
			return "", ErrAgentdPolicy
		}
		return "AGENTD", nil
	}
	if f.RequestDigest != "" {
		return "", ErrAgentdPolicy
	}
	if h := f.GetHeartbeat(); h != nil {
		if h.ProcessState < agentdv1.Heartbeat_ABSENT || h.ProcessState > agentdv1.Heartbeat_FAILED {
			return "", ErrAgentdPolicy
		}
		return "AGENTD", nil
	}
	if o := f.GetObservation(); o != nil {
		if control.IsShutdownObservation(f) {
			return "AGENTD", nil
		}
		if f.HarnessHandle != m.HarnessHandle || o.ArtifactReference != "" {
			return "", ErrAgentdPolicy
		}
		if o.Kind == agentdv1.Observation_DIAGNOSTIC && (o.PayloadSchema == control.Capability+"/stdout" || o.PayloadSchema == control.Capability+"/stderr") && len(o.Payload) <= int(m.Config.Limits.DiagnosticBytes) {
			return "AGENTD", nil
		}
		if o.Kind == agentdv1.Observation_CANDIDATE_EVENT && o.PayloadSchema == control.Capability+"/event" && len(o.Payload) <= int(m.Config.Limits.EventBytes) {
			return "AGENTD", nil
		}
	}
	if r := f.GetRotation(); r != nil {
		if r.Kind == agentdv1.Rotation_REQUEST && len(r.CertificatePem) == 0 && len(r.PrivateKeyPem) == 0 && r.ExpiresUnixMs == 0 {
			return "AGENTD", nil
		}
		if r.Kind == agentdv1.Rotation_ISSUED && len(r.CertificatePem) > 0 && len(r.CertificatePem) <= 16384 && len(r.PrivateKeyPem) > 0 && len(r.PrivateKeyPem) <= 4096 && r.ExpiresUnixMs > time.Now().UnixMilli() && r.ExpiresUnixMs <= m.Deadline.UnixMilli() {
			return "AR", nil
		}
	}
	return "", ErrAgentdPolicy
}
func policySequence(ctx context.Context, tx *sql.Tx, s sandbox.Scope, f *agentdv1.Envelope, direction string) error {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(f)
	if err != nil {
		return err
	}
	digest := sandbox.Digest(raw)
	clear(raw)
	var sequence uint64
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT sequence,frame_digest FROM agentd_frame_sequences WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND connection_id=$3 AND direction=$4`, s.TenantID, s.SandboxID, f.ConnectionId, direction).Scan(&sequence, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if f.Sequence == sequence && digest == previous {
		return nil
	}
	if f.Sequence != sequence+1 {
		return ErrAgentdPolicy
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agentd_frame_sequences(tenant_id,sandbox_binding_id,connection_id,direction,sequence,frame_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,sandbox_binding_id,connection_id,direction) DO UPDATE SET sequence=EXCLUDED.sequence,frame_digest=EXCLUDED.frame_digest`, s.TenantID, s.SandboxID, f.ConnectionId, direction, f.Sequence, digest)
	return err
}

// CommandOutcome reads retained bookkeeping even after revocation/termination.
// PENDING means outcome unknown, not permission to send again.
func (p *AgentdPolicy) CommandOutcome(ctx context.Context, tenant, sandboxID, operation primitives.ID, digest string) (transport.DispatchOutcome, error) {
	var outcome transport.DispatchOutcome
	err := p.credentials.bindings.transaction(ctx, tenant, func(tx *sql.Tx) error {
		var saved string
		err := tx.QueryRowContext(ctx, `SELECT outcome,request_digest FROM agentd_commands WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND operation_id=$3`, tenant, sandboxID, operation).Scan(&outcome, &saved)
		if err != nil {
			return err
		}
		if saved != digest {
			return sandbox.ErrConflict
		}
		return nil
	})
	if errors.Is(err, sandbox.ErrNotFound) {
		return "", transport.ErrCommandNotFound
	}
	if err != nil {
		return "", ErrAgentdPolicy
	}
	return outcome, nil
}
