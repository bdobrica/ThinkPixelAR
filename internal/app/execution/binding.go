package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

var ErrInvalidBinding = errors.New("invalid Execution authority/runtime binding")

// LoadLocalBinding reconstructs the original grant from an Execution's durable
// reference. Repositories must belong to a trusted, authorized tenant transaction.
// No operator catalog lookup or new admission occurs. Historical bindings remain
// readable after expiry/cancellation and after the Session advances generations.
//
// This is integrity verification, NOT permission for forward work. Consumers must
// still call LocalLifecycle.Validate and check current Session/Attempt fences at
// the mutation boundary. Do not trust a queue payload or harness-supplied grant.
func LoadLocalBinding(ctx context.Context, repos persistence.Repositories, id primitives.ID) (*domain.Execution, authority.Grant, error) {
	e, err := repos.Executions().Get(ctx, id)
	if err != nil {
		return nil, authority.Grant{}, err
	}
	b := e.Binding()
	if b.AuthorityMode != "LOCAL" || b.AuthorityNamespace != authority.LocalIssuer {
		return nil, authority.Grant{}, ErrUnsupported
	}
	grantID, err := primitives.ParseID(b.AuthorityReference)
	if err != nil {
		return nil, authority.Grant{}, ErrInvalidBinding
	}
	s, err := repos.Sessions().Get(ctx, b.SessionID)
	if err != nil {
		return nil, authority.Grant{}, err
	}
	record, err := repos.LocalGrants().Get(ctx, grantID)
	if err != nil {
		return nil, authority.Grant{}, err
	}
	g, err := verifyLocalBinding(e, s, record)
	if err != nil {
		return nil, authority.Grant{}, err
	}
	return e, g, nil
}

// Used both before admission commits and when reconstructing after restart.
func verifyLocalBinding(e *domain.Execution, s *session.Session, record persistence.LocalGrantRecord) (authority.Grant, error) {
	invalid := authority.Grant{}
	var g authority.Grant
	if e == nil || s == nil || len(record.Snapshot) == 0 || len(record.Snapshot) > 65536 ||
		sandbox.Digest(record.Snapshot) != record.Digest || json.Unmarshal(record.Snapshot, &g) != nil {
		return invalid, ErrInvalidBinding
	}
	// Local grants retain exact issuance bytes. Reject unknown fields or a
	// snapshot that would be reinterpreted by decoding/re-encoding its schema.
	raw, err := json.Marshal(g)
	if err != nil || !bytes.Equal(raw, record.Snapshot) {
		return invalid, ErrInvalidBinding
	}
	b := e.Binding()
	if g.Mode != authority.LocalMode || g.Issuer != authority.LocalIssuer ||
		b.AuthorityMode != "LOCAL" || b.AuthorityNamespace != g.Issuer || b.ExternalRunID != "" ||
		b.AuthorityMode != g.Runtime.AuthorityMode || b.AuthorityNamespace != g.Runtime.AuthorityNamespace ||
		b.AuthorityReference != string(g.ID) || record.ID != g.ID || b.GrantDigest != record.Digest ||
		g.TenantID != e.TenantID() || s.TenantID() != e.TenantID() ||
		g.SessionID != b.SessionID || s.ID() != b.SessionID || record.SessionID != b.SessionID ||
		g.Generation != b.SessionGeneration || g.SessionVersion == 0 ||
		!digestPattern.MatchString(g.PrincipalDigest) || !digestPattern.MatchString(g.RequestDigest) ||
		!sameRuntime(g.Runtime, s.Binding()) || b.AgentID != g.Runtime.AgentID || b.AgentVersionID != g.Runtime.AgentVersionID ||
		!e.Deadline().Truncate(time.Microsecond).Equal(g.ExpiresAt.Truncate(time.Microsecond)) ||
		g.IssuedAt.IsZero() || !g.ExpiresAt.After(g.IssuedAt) ||
		e.CreatedAt().Truncate(time.Microsecond).Before(g.IssuedAt.Truncate(time.Microsecond)) {
		return invalid, ErrInvalidBinding
	}
	// agent_evidence is JSONB: PostgreSQL changes object ordering/whitespace.
	// Rebuild the exact typed encoding used by EXE-001 before checking its digest,
	// and compare canonical objects as well so unknown fields cannot be ignored.
	var runtime session.RuntimeBinding
	if json.Unmarshal(b.AgentEvidence, &runtime) != nil || !sameRuntime(runtime, g.Runtime) {
		return invalid, ErrInvalidBinding
	}
	raw, err = json.Marshal(runtime)
	if err != nil || sandbox.Digest(raw) != b.AgentEvidenceDigest {
		return invalid, ErrInvalidBinding
	}
	want, err := canonical.Transform(raw)
	if err != nil {
		return invalid, ErrInvalidBinding
	}
	got, err := canonical.Transform(b.AgentEvidence)
	if err != nil || !bytes.Equal(want, got) {
		return invalid, ErrInvalidBinding
	}
	return g, nil
}
