package bootstrap

import (
	"context"
	"time"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// DeliveryEntry contains no bootstrap material. Cleanup is irrevocable; absence
// after an ambiguous publication remains pending until an exact object is deleted.
type DeliveryEntry struct {
	Reference                 Reference
	CleanupRequested, Cleaned bool
}

// Journal commits before returning. SavePlan is an immutable, single-winner publication claim; BindUID
// binds once. Every operation is tenant-scoped, including cleanup after expiry.
type Journal interface {
	SavePlan(context.Context, Reference) error
	LoadDelivery(context.Context, primitives.ID, primitives.ID) (DeliveryEntry, error)
	BindUID(context.Context, Reference) error
	RequestCleanup(context.Context, primitives.ID, primitives.ID) error
	DueCleanup(context.Context, primitives.ID, int) ([]DeliveryEntry, error)
	CompleteCleanup(context.Context, Reference) error
}

type Delivery struct {
	secrets   *Store
	journal   Journal
	authorize func(context.Context, transport.CredentialRecord) error
}

func NewDelivery(secrets *Store, journal Journal, authorize func(context.Context, transport.CredentialRecord) error) (*Delivery, error) {
	if secrets == nil || journal == nil || authorize == nil {
		return nil, ErrBootstrap
	}
	return &Delivery{secrets, journal, authorize}, nil
}

// Publish durably reserves cleanup before Kubernetes mutation and records UID
// before returning a projection name. Material is never passed to the journal.
func (d *Delivery) Publish(ctx context.Context, r transport.CredentialRecord, m Material) (name string, err error) {
	if d.authorize(ctx, r) != nil {
		return "", ErrBootstrap
	}
	planned, err := d.secrets.Plan(r, m)
	if err != nil {
		return "", ErrBootstrap
	}
	if d.journal.SavePlan(ctx, planned) != nil {
		return "", ErrBootstrap
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.journal.RequestCleanup(cleanup, r.Identity.TenantID, r.CredentialID)
		}
	}()
	entry, err := d.journal.LoadDelivery(ctx, r.Identity.TenantID, r.CredentialID)
	if err != nil || entry.Cleaned || entry.CleanupRequested {
		return "", ErrBootstrap
	}
	ref, err := d.secrets.Publish(ctx, r, m)
	if err != nil {
		return "", ErrBootstrap
	}
	if d.journal.BindUID(ctx, ref) != nil {
		return "", ErrBootstrap
	}
	entry, err = d.journal.LoadDelivery(ctx, r.Identity.TenantID, r.CredentialID)
	if err != nil || entry.Cleaned || entry.CleanupRequested || entry.Reference != ref || d.authorize(ctx, r) != nil {
		return "", ErrBootstrap
	}
	return d.secrets.Resolve(ctx, entry.Reference)
}

// Cleanup is safe after acceptance, cancellation or expiry and needs no renewed
// execution authority. Errors leave durable work eligible for retry.
func (d *Delivery) Cleanup(ctx context.Context, tenant, credential primitives.ID) error {
	if d.journal.RequestCleanup(ctx, tenant, credential) != nil {
		return ErrBootstrap
	}
	entry, err := d.journal.LoadDelivery(ctx, tenant, credential)
	if err != nil {
		return ErrBootstrap
	}
	if entry.Cleaned {
		return nil
	}
	ref := entry.Reference
	if ref.UID == "" {
		ref, err = d.secrets.Recover(ctx, ref)
		// A delayed create can still arrive after NotFound. Never forget this plan.
		if err != nil {
			return err
		}
		if d.journal.BindUID(ctx, ref) != nil {
			return ErrBootstrap
		}
	}
	if d.secrets.Delete(ctx, ref) != nil {
		return ErrBootstrap
	}
	return d.journal.CompleteCleanup(ctx, ref)
}

// Sweep is a bounded tenant-scoped cleanup pass. Expiry eligibility is evaluated
// by the database clock. Call periodically and after admission/failure signals.
func (d *Delivery) Sweep(ctx context.Context, tenant primitives.ID, limit int) error {
	if limit < 1 || limit > 128 {
		return ErrBootstrap
	}
	entries, err := d.journal.DueCleanup(ctx, tenant, limit)
	if err != nil {
		return ErrBootstrap
	}
	var result error
	for _, entry := range entries {
		if err := d.Cleanup(ctx, tenant, entry.Reference.Record.CredentialID); err != nil {
			result = ErrBootstrap
		}
		if ctx.Err() != nil {
			return ErrBootstrap
		}
	}
	return result
}
