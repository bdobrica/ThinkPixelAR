package postgres

import (
	"context"
	"database/sql"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
)

// CheckBootstrap gates Secret publication/lookup against the exact registered,
// unconsumed bootstrap and current persisted execution fence. It is not renewed
// Run authority, and is intentionally not required for cleanup after cancellation.
func (s *AgentdCredentials) CheckBootstrap(ctx context.Context, c transport.CredentialRecord) error {
	if !c.Bootstrap {
		return transport.ErrCredentialState
	}
	return s.transaction(ctx, c.Identity, func(tx *sql.Tx, _ sandbox.Binding) error {
		var current bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agentd_credentials c JOIN agentd_credential_state s USING(tenant_id,sandbox_binding_id) WHERE c.tenant_id=$1 AND c.sandbox_binding_id=$2 AND c.credential_id=$3 AND c.certificate_digest=$4 AND c.issuer_digest=$5 AND c.proof_digest=$6 AND c.not_before=$7 AND c.expires_at=$8 AND c.bootstrap AND c.consumed_at IS NULL AND c.not_before<=clock_timestamp() AND c.expires_at>clock_timestamp() AND s.latest_digest=c.certificate_digest)`, c.Identity.TenantID, c.Identity.SandboxID, c.CredentialID, c.CertificateDigest, c.IssuerDigest, c.ProofDigest, c.NotBefore, c.ExpiresAt).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return transport.ErrCredentialState
		}
		return nil
	})
}
