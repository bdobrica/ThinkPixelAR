package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// AgentdCredentials persists identity bookkeeping only. It is deliberately not
// an Authorizer: provider/Run checks and wire expectations need trusted composition.
type AgentdCredentials struct{ bindings *SandboxBindings }

var _ transport.CredentialRegistry = (*AgentdCredentials)(nil)

func NewAgentdCredentials(db *sql.DB) (*AgentdCredentials, error) {
	b, err := NewSandboxBindings(db)
	if err != nil {
		return nil, transport.ErrCredentialState
	}
	return &AgentdCredentials{bindings: b}, nil
}
func (s *AgentdCredentials) transaction(ctx context.Context, id transport.Identity, work func(*sql.Tx, sandbox.Binding) error) error {
	for _, v := range []primitives.ID{id.TenantID, id.SandboxID, id.AttemptID} {
		if _, err := primitives.ParseID(string(v)); err != nil {
			return transport.ErrCredentialState
		}
	}
	err := s.bindings.transaction(ctx, id.TenantID, func(tx *sql.Tx) error {
		// Read immutable request first, then lock aggregates before binding rows,
		// matching the provider's lock order and fencing concurrent cancellation.
		b, _, err := readSandboxBinding(ctx, tx, id.TenantID, id.SandboxID, false)
		if err != nil {
			return err
		}
		if b.Request.Scope.AttemptID != id.AttemptID {
			return transport.ErrCredentialState
		}
		current, err := lockSandboxFence(ctx, tx, b.Request)
		if err != nil {
			return err
		}
		if !current {
			return transport.ErrCredentialState
		}
		b, _, err = readSandboxBinding(ctx, tx, id.TenantID, id.SandboxID, true)
		if err != nil {
			return err
		}
		var allowed bool
		if err = tx.QueryRowContext(ctx, `SELECT release_operation_id IS NULL AND state NOT IN ('RELEASING','RELEASED','FAILED','SUSPENDED','SUSPENDING','UNKNOWN') FROM sandbox_bindings WHERE tenant_id=$1 AND sandbox_binding_id=$2`, id.TenantID, id.SandboxID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return transport.ErrCredentialState
		}
		return work(tx, b)
	})
	if err != nil {
		return transport.ErrCredentialState
	}
	return nil
}
func (s *AgentdCredentials) Version(ctx context.Context, id transport.Identity) (uint64, error) {
	var v uint64
	err := s.transaction(ctx, id, func(tx *sql.Tx, _ sandbox.Binding) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agentd_credential_state(tenant_id,sandbox_binding_id,attempt_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id.TenantID, id.SandboxID, id.AttemptID)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT version FROM agentd_credential_state WHERE tenant_id=$1 AND sandbox_binding_id=$2`, id.TenantID, id.SandboxID).Scan(&v)
	})
	return v, err
}
func credentialDigest(v string) bool {
	if len(v) != 71 || v[:7] != "sha256:" {
		return false
	}
	b, err := hex.DecodeString(v[7:])
	return err == nil && hex.EncodeToString(b) == v[7:]
}
func (s *AgentdCredentials) Register(ctx context.Context, r transport.CredentialRequest, g transport.CredentialGrant, c transport.CredentialRecord) error {
	now := time.Now()
	ttl := 15 * time.Minute
	if c.Bootstrap {
		ttl = 10 * time.Minute
	}
	if _, err := primitives.ParseID(string(c.CredentialID)); err != nil {
		return transport.ErrCredentialState
	}
	if r.Identity != g.Identity || r.Identity != c.Identity || g.Version == 0 || !credentialDigest(c.CertificateDigest) || !credentialDigest(c.IssuerDigest) || c.NotBefore.After(now) || !c.ExpiresAt.After(now) || c.ExpiresAt.Sub(c.NotBefore) > ttl || !c.ExpiresAt.After(c.NotBefore) || c.ExpiresAt.After(g.AuthorityDeadline) || c.ExpiresAt.After(g.AttemptDeadline) {
		return transport.ErrCredentialState
	}
	if c.Bootstrap {
		if r.Epoch != 0 || r.ConnectionID != "" || r.Peer != (transport.Peer{}) || !credentialDigest(c.ProofDigest) || c.ExpiresAt.After(g.BootstrapDeadline) {
			return transport.ErrCredentialState
		}
	} else {
		if r.Recovery || r.Peer.Identity != r.Identity || r.Epoch == 0 || !r.Peer.ExpiresAt.After(now) || c.ProofDigest != "" {
			return transport.ErrCredentialState
		}
	}
	return s.transaction(ctx, r.Identity, func(tx *sql.Tx, b sandbox.Binding) error {
		if c.ExpiresAt.After(b.Request.Deadline) {
			return transport.ErrCredentialState
		}
		var v uint64
		var latest string
		if err := tx.QueryRowContext(ctx, `SELECT version,COALESCE(latest_digest,'') FROM agentd_credential_state WHERE tenant_id=$1 AND sandbox_binding_id=$2 FOR UPDATE`, r.Identity.TenantID, r.Identity.SandboxID).Scan(&v, &latest); err != nil {
			return err
		}
		if v != g.Version || c.Bootstrap && !r.Recovery && latest != "" {
			return transport.ErrCredentialState
		}
		if r.Recovery {
			var expired bool
			if latest == "" || b.ProviderReference == "" {
				return transport.ErrCredentialState
			}
			if err := tx.QueryRowContext(ctx, `SELECT c.expires_at<=clock_timestamp() AND (s.connection_deadline IS NULL OR s.connection_deadline<=clock_timestamp()) FROM agentd_credentials c JOIN agentd_credential_state s USING(tenant_id,sandbox_binding_id) WHERE c.tenant_id=$1 AND c.sandbox_binding_id=$2 AND c.certificate_digest=s.latest_digest`, r.Identity.TenantID, r.Identity.SandboxID).Scan(&expired); err != nil || !expired {
				return transport.ErrCredentialState
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agentd_credential_state SET connection_id=NULL,connection_digest=NULL,connection_deadline=NULL,connection_epoch=connection_epoch+1 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, r.Identity.TenantID, r.Identity.SandboxID); err != nil {
				return err
			}
		}
		if !c.Bootstrap {
			if latest != r.Peer.CertificateDigest {
				return transport.ErrCredentialState
			}
			if err := checkAgentdConnection(ctx, tx, r.Peer, transport.Connection{ID: r.ConnectionID, Epoch: r.Epoch}); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO agentd_credentials(tenant_id,sandbox_binding_id,credential_id,certificate_digest,issuer_digest,proof_digest,bootstrap,not_before,expires_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 WHERE $9 > clock_timestamp()`, r.Identity.TenantID, r.Identity.SandboxID, c.CredentialID, c.CertificateDigest, c.IssuerDigest, nullString(c.ProofDigest), c.Bootstrap, c.NotBefore, c.ExpiresAt)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return transport.ErrCredentialState
		}
		if !c.Bootstrap {
			if _, err := tx.ExecContext(ctx, `UPDATE agentd_credential_state SET connection_deadline=LEAST(connection_deadline,clock_timestamp()+interval '30 seconds') WHERE tenant_id=$1 AND sandbox_binding_id=$2`, r.Identity.TenantID, r.Identity.SandboxID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE agentd_credential_state SET version=version+1,latest_digest=$3,issued_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_binding_id=$2`, r.Identity.TenantID, r.Identity.SandboxID, c.CertificateDigest)
		return err
	})
}
func (s *AgentdCredentials) ConsumeBootstrap(ctx context.Context, p transport.Peer, proof []byte, deadline time.Time) (transport.Connection, error) {
	fail := transport.Connection{}
	if len(proof) != 32 || !credentialDigest(p.CertificateDigest) || !deadline.After(time.Now()) || deadline.After(p.ExpiresAt) {
		return fail, transport.ErrCredentialState
	}
	hash := sha256.Sum256(proof)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	id, err := primitives.NewID(time.Now())
	if err != nil {
		return fail, transport.ErrCredentialState
	}
	c := transport.Connection{ID: id, Deadline: deadline}
	err = s.transaction(ctx, p.Identity, func(tx *sql.Tx, b sandbox.Binding) error {
		if deadline.After(b.Request.Deadline) || b.ProviderReference == "" {
			return transport.ErrCredentialState
		}
		var expected string
		err := tx.QueryRowContext(ctx, `SELECT c.proof_digest FROM agentd_credentials c JOIN agentd_credential_state s USING(tenant_id,sandbox_binding_id) WHERE c.tenant_id=$1 AND c.sandbox_binding_id=$2 AND c.certificate_digest=$3 AND s.latest_digest=c.certificate_digest AND c.bootstrap AND c.consumed_at IS NULL AND c.not_before<=clock_timestamp() AND c.expires_at>clock_timestamp() AND c.expires_at=$4 AND s.connection_id IS NULL FOR UPDATE OF c,s`, p.Identity.TenantID, p.Identity.SandboxID, p.CertificateDigest, p.ExpiresAt).Scan(&expected)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(digest)) != 1 {
			return transport.ErrCredentialState
		}
		_, err = tx.ExecContext(ctx, `UPDATE agentd_credentials SET consumed_at=clock_timestamp() WHERE tenant_id=$1 AND certificate_digest=$2`, p.Identity.TenantID, p.CertificateDigest)
		if err != nil {
			return err
		}
		// Consumption and cleanup intent commit together. Invalid proofs never
		// reach this update. A failed post-admission check cannot lose cleanup.
		if _, err = tx.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery d SET cleanup_requested=true,retry_after=LEAST(d.retry_after,clock_timestamp()) FROM agentd_credentials r WHERE d.tenant_id=$1 AND d.credential_id=r.credential_id AND r.tenant_id=d.tenant_id AND r.certificate_digest=$2 AND NOT d.cleaned`, p.Identity.TenantID, p.CertificateDigest); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `UPDATE agentd_credential_state SET version=version+1,connection_id=$3,connection_epoch=connection_epoch+1,connection_digest=$4,connection_deadline=$5 WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND $5>clock_timestamp() RETURNING connection_epoch`, p.Identity.TenantID, p.Identity.SandboxID, c.ID, p.CertificateDigest, deadline).Scan(&c.Epoch)
	})
	if err != nil {
		return fail, err
	}
	return c, nil
}
func checkAgentdConnection(ctx context.Context, tx *sql.Tx, p transport.Peer, c transport.Connection) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT s.connection_id=$3 AND s.connection_epoch=$4 AND s.connection_digest=$5 AND s.connection_deadline>clock_timestamp() AND c.expires_at=$6 AND c.expires_at>clock_timestamp() FROM agentd_credential_state s JOIN agentd_credentials c ON c.tenant_id=s.tenant_id AND c.sandbox_binding_id=s.sandbox_binding_id AND c.certificate_digest=s.connection_digest WHERE s.tenant_id=$1 AND s.sandbox_binding_id=$2`, p.Identity.TenantID, p.Identity.SandboxID, c.ID, c.Epoch, p.CertificateDigest, p.ExpiresAt).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return transport.ErrCredentialState
	}
	return nil
}
func (s *AgentdCredentials) CheckConnection(ctx context.Context, p transport.Peer, c transport.Connection) error {
	return s.transaction(ctx, p.Identity, func(tx *sql.Tx, _ sandbox.Binding) error { return checkAgentdConnection(ctx, tx, p, c) })
}

// IsSessionCredential lets the host defer its operator command plan until the
// bootstrap-to-session rotation has completed. This read grants no authority;
// all later frames still require current connection and execution admission.
func (s *AgentdCredentials) IsSessionCredential(ctx context.Context, peer transport.Peer) (bool, error) {
	var session bool
	err := s.transaction(ctx, peer.Identity, func(tx *sql.Tx, _ sandbox.Binding) error {
		return tx.QueryRowContext(ctx, `SELECT NOT bootstrap FROM agentd_credentials WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND certificate_digest=$3 AND expires_at>clock_timestamp()`, peer.Identity.TenantID, peer.Identity.SandboxID, peer.CertificateDigest).Scan(&session)
	})
	return session, err
}

// Close is cleanup: it remains allowed after cancellation, but only for this epoch.
func (s *AgentdCredentials) CloseConnection(ctx context.Context, id transport.Identity, c transport.Connection) error {
	err := s.bindings.transaction(ctx, id.TenantID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agentd_credential_state SET version=version+1,connection_id=NULL,connection_digest=NULL,connection_deadline=NULL WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND attempt_id=$3 AND connection_id=$4 AND connection_epoch=$5`, id.TenantID, id.SandboxID, id.AttemptID, c.ID, c.Epoch)
		return err
	})
	if err != nil {
		return transport.ErrCredentialState
	}
	return nil
}

// Reconnect replaces an epoch only with the latest registered unexpired identity.
// Old Close callbacks cannot clear the replacement's different ID/epoch.
func (s *AgentdCredentials) Reconnect(ctx context.Context, p transport.Peer, deadline time.Time) (transport.Connection, error) {
	if !credentialDigest(p.CertificateDigest) || !deadline.After(time.Now()) || deadline.After(p.ExpiresAt) {
		return transport.Connection{}, transport.ErrCredentialState
	}
	id, err := primitives.NewID(time.Now())
	if err != nil {
		return transport.Connection{}, transport.ErrCredentialState
	}
	c := transport.Connection{ID: id, Deadline: deadline}
	err = s.transaction(ctx, p.Identity, func(tx *sql.Tx, b sandbox.Binding) error {
		if b.ProviderReference == "" || deadline.After(b.Request.Deadline) {
			return transport.ErrCredentialState
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT c.not_before<=clock_timestamp() AND c.expires_at>clock_timestamp() AND c.expires_at=$4 AND (NOT c.bootstrap OR c.consumed_at IS NOT NULL) FROM agentd_credential_state s JOIN agentd_credentials c ON c.tenant_id=s.tenant_id AND c.sandbox_binding_id=s.sandbox_binding_id AND c.certificate_digest=s.latest_digest WHERE s.tenant_id=$1 AND s.sandbox_binding_id=$2 AND s.latest_digest=$3 FOR UPDATE OF s`, p.Identity.TenantID, p.Identity.SandboxID, p.CertificateDigest, p.ExpiresAt).Scan(&valid); err != nil || !valid {
			return transport.ErrCredentialState
		}
		return tx.QueryRowContext(ctx, `UPDATE agentd_credential_state SET version=version+1,connection_id=$3,connection_epoch=connection_epoch+1,connection_digest=$4,connection_deadline=$5 WHERE tenant_id=$1 AND sandbox_binding_id=$2 AND $5>clock_timestamp() RETURNING connection_epoch`, p.Identity.TenantID, p.Identity.SandboxID, c.ID, p.CertificateDigest, deadline).Scan(&c.Epoch)
	})
	if err != nil {
		return transport.Connection{}, err
	}
	return c, nil
}
