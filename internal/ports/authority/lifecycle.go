package authority

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidGrant = errors.New("authority: INVALID_GRANT")

type State string

const (
	Active    State = "ACTIVE"
	Cancelled State = "CANCELLED"
	Expired   State = "EXPIRED"
)

type Status struct {
	State      State
	ObservedAt time.Time
}

// LocalLifecycle verifies issuance integrity and cancellation/expiry only.
// Caller must come from trusted authentication and Session authorization.
// Consumers must additionally compare the snapshot with their persisted
// Execution binding and check current Session/Attempt fences. Only ACTIVE
// permits work; errors never imply authority. This is not full RunAuthority.
type LocalLifecycle interface {
	Validate(context.Context, Caller, Grant) (Status, error)
	Cancel(context.Context, Caller, Grant) error
}
