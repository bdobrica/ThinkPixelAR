package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

var _ authority.LocalLifecycle = (*Authority)(nil)

func (a *Authority) Validate(ctx context.Context, c authority.Caller, g authority.Grant) (result authority.Status, resultErr error) {
	ctx, finish := a.observe(ctx, "validate")
	defer func() { finish(resultErr, result.State) }()
	return a.status(ctx, c, g, false)
}

// Cancel requires the issuing principal plus trusted Session authorization.
// Administrative delegation is intentionally not inferred from request fields.
func (a *Authority) Cancel(ctx context.Context, c authority.Caller, g authority.Grant) (resultErr error) {
	ctx, finish := a.observe(ctx, "cancel")
	var result authority.Status
	defer func() { finish(resultErr, result.State) }()
	result, resultErr = a.status(ctx, c, g, true)
	return resultErr
}

func (a *Authority) status(ctx context.Context, c authority.Caller, g authority.Grant, cancel bool) (authority.Status, error) {
	var result authority.Status
	if !validID(string(c.TenantID)) || !digestPattern.MatchString(c.PrincipalDigest) {
		return result, authority.ErrDenied
	}
	if !validID(string(g.ID)) || c.TenantID != g.TenantID || c.PrincipalDigest != g.PrincipalDigest || g.Mode != "local" || g.Issuer != "thinkpixelar/local" {
		return result, authority.ErrInvalidGrant
	}
	payload, err := json.Marshal(g)
	if err != nil || len(payload) > 65536 {
		return result, authority.ErrInvalidGrant
	}
	err = a.store.WithinTransaction(ctx, c.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		record, err := repos.LocalGrants().Get(ctx, g.ID)
		if errors.Is(err, persistence.ErrNotFound) {
			return authority.ErrInvalidGrant
		}
		if err != nil {
			return err
		}
		if record.ID != g.ID || record.SessionID != g.SessionID || sandbox.Digest(record.Snapshot) != record.Digest || !bytes.Equal(payload, record.Snapshot) {
			return authority.ErrInvalidGrant
		}
		// Read the trusted clock after acquiring the status lock, never before a
		// possibly long wait. Config reload does not reinterpret issued ceilings.
		now := a.clock.Now().UTC()
		if now.IsZero() {
			return authority.ErrUnavailable
		}
		if now.Before(g.IssuedAt) || !g.ExpiresAt.After(g.IssuedAt) {
			return authority.ErrInvalidGrant
		}
		state := authority.State(record.State)
		switch state {
		case authority.Active:
			if record.Version != 0 {
				return authority.ErrInvalidGrant
			}
			if !now.Before(g.ExpiresAt) {
				state = authority.Expired
			} else if cancel {
				state = authority.Cancelled
			}
			if state != authority.Active {
				if err := repos.LocalGrants().Transition(ctx, g.ID, record.Version, string(state), now); err != nil {
					return err
				}
			}
		case authority.Cancelled, authority.Expired:
			if record.Version != 1 {
				return authority.ErrInvalidGrant
			}
		default:
			return authority.ErrInvalidGrant
		}
		result = authority.Status{State: state, ObservedAt: now}
		return nil
	})
	if err != nil {
		if errors.Is(err, authority.ErrInvalidGrant) {
			return authority.Status{}, authority.ErrInvalidGrant
		}
		return authority.Status{}, authority.ErrUnavailable
	}
	return result, nil
}
