// Package agentdbootstrap composes issuance, durable publication and bounded
// cleanup. Only trusted materialization code supplies configuration/projection.
package agentdbootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrBootstrap = errors.New("agentd bootstrap materialization failed")

type Issuer interface {
	Bootstrap(context.Context, transport.Identity) (agentdidentity.Delivery, error)
}
type Delivery interface {
	Publish(context.Context, transport.CredentialRecord, bootstrap.Material) (string, error)
	Cleanup(context.Context, primitives.ID, primitives.ID) error
}
type Service struct {
	issuer   Issuer
	delivery Delivery
}

func New(issuer Issuer, delivery Delivery) (*Service, error) {
	if issuer == nil || delivery == nil {
		return nil, ErrBootstrap
	}
	return &Service{issuer, delivery}, nil
}

// Materialize issues once, publishes through the durable coordinator, then gives
// trusted projection code only the resolved Secret name. project must honor its
// context and durably bind the projection before acquisition. It may not log or
// retain material. Config/challenge must match registered trusted materialization.
// A failed/ambiguous call is reconciled, never automatically reissued here.
func (s *Service) Materialize(ctx context.Context, id transport.Identity, config, serverCA, challenge []byte, project func(context.Context, string) error) error {
	if project == nil || ctx.Err() != nil {
		return ErrBootstrap
	}
	issued, err := s.issuer.Bootstrap(ctx, id)
	if err != nil {
		issued.Destroy()
		return ErrBootstrap
	}
	defer issued.Destroy()
	if issued.Record.Identity != id || !issued.Record.Bootstrap || !issued.Record.ExpiresAt.After(time.Now()) {
		return ErrBootstrap
	}
	success := false
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Delivery persists intent before deletion; expiry is a crash fallback even
			// if this best-effort immediate cleanup fails or the caller was cancelled.
			_ = s.delivery.Cleanup(cleanup, id.TenantID, issued.Record.CredentialID)
		}
	}()
	call, cancel := context.WithDeadline(ctx, issued.Record.ExpiresAt)
	defer cancel()
	material := bootstrap.Material{Config: config, ServerCA: serverCA, Challenge: challenge, Certificate: issued.Certificate.CertificatePEM, PrivateKey: issued.Certificate.PrivateKeyPEM, Proof: issued.Proof}
	name, err := s.delivery.Publish(call, issued.Record, material)
	if err != nil || name == "" || call.Err() != nil {
		return ErrBootstrap
	}
	if project(call, name) != nil || call.Err() != nil {
		return ErrBootstrap
	}
	success = true
	return nil
}
