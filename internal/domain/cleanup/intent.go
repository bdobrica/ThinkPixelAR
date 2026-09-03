// Package cleanup models exact, retryable external-resource cleanup intent.
package cleanup

import (
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type State string

const (
	Pending     State = "PENDING"
	Confirmed   State = "CONFIRMED"
	Quarantined State = "QUARANTINED"
)

var (
	ErrInvalidIntent   = errors.New("invalid cleanup intent")
	ErrVersionConflict = errors.New("cleanup intent version conflict")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	namePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
)

// Target is immutable, credential-free evidence identifying one exact
// external resource. OwnershipProofDigest is evidence for safe deletion, not
// authorization by itself.
type Target struct {
	OwnerType, TargetType, ProviderKind, ExternalReference string
	OwnerID, OperationID                                   primitives.ID
	RequestDigest, OwnershipProofDigest                    string
	Orphan                                                 bool
}

// Intent remains as a tombstone after confirmed absence, preventing an
// ambiguous external operation from being retried under a fresh identity.
type Intent struct {
	tenantID, id               primitives.ID
	target                     Target
	state                      State
	version, attempts          uint64
	nextAttemptAt              time.Time
	lastErrorCode              string
	createdAt, updatedAt       time.Time
	confirmedAt, quarantinedAt *time.Time
}

func New(tenantID, id primitives.ID, target Target, nextAttemptAt, now time.Time) (*Intent, error) {
	i := &Intent{tenantID: tenantID, id: id, target: target, state: Pending,
		nextAttemptAt: nextAttemptAt.UTC(), createdAt: now.UTC(), updatedAt: now.UTC()}
	if !i.valid() || now.IsZero() || nextAttemptAt.IsZero() {
		return nil, ErrInvalidIntent
	}
	return i, nil
}

func Restore(tenantID, id primitives.ID, target Target, state State, version, attempts uint64,
	nextAttemptAt time.Time, lastErrorCode string, createdAt, updatedAt time.Time,
	confirmedAt, quarantinedAt *time.Time) (*Intent, error) {
	i := &Intent{tenantID: tenantID, id: id, target: target, state: state, version: version,
		attempts: attempts, nextAttemptAt: nextAttemptAt.UTC(), lastErrorCode: lastErrorCode,
		createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC(), confirmedAt: copyTime(confirmedAt), quarantinedAt: copyTime(quarantinedAt)}
	if !i.valid() {
		return nil, ErrInvalidIntent
	}
	return i, nil
}

func (i *Intent) Retry(expected uint64, next time.Time, errorCode string, now time.Time) error {
	if err := i.mutable(expected, now); err != nil {
		return err
	}
	if !next.After(now) || !boundedName(errorCode, 128) {
		return ErrInvalidIntent
	}
	i.attempts++
	i.version++
	i.nextAttemptAt = next.UTC()
	i.lastErrorCode = errorCode
	i.updatedAt = now.UTC()
	return nil
}

func (i *Intent) Confirm(expected uint64, now time.Time) error {
	if err := i.mutable(expected, now); err != nil {
		return err
	}
	i.attempts++
	i.version++
	i.state = Confirmed
	i.lastErrorCode = ""
	i.updatedAt = now.UTC()
	at := i.updatedAt
	i.confirmedAt = &at
	return nil
}

func (i *Intent) Quarantine(expected uint64, reasonCode string, now time.Time) error {
	if err := i.mutable(expected, now); err != nil {
		return err
	}
	if !boundedName(reasonCode, 128) {
		return ErrInvalidIntent
	}
	i.attempts++
	i.version++
	i.state = Quarantined
	i.lastErrorCode = reasonCode
	i.updatedAt = now.UTC()
	at := i.updatedAt
	i.quarantinedAt = &at
	return nil
}

func (i *Intent) mutable(expected uint64, now time.Time) error {
	if i == nil || expected != i.version {
		return ErrVersionConflict
	}
	if i.state != Pending || now.IsZero() || now.UTC().Before(i.updatedAt) {
		return ErrInvalidIntent
	}
	return nil
}

func (i *Intent) valid() bool {
	if !validID(i.tenantID) || !validID(i.id) || !validID(i.target.OwnerID) || !validID(i.target.OperationID) ||
		!boundedName(i.target.OwnerType, 64) || !boundedName(i.target.TargetType, 64) || !boundedName(i.target.ProviderKind, 128) ||
		!bounded(i.target.ExternalReference, 2048) || !digestPattern.MatchString(i.target.RequestDigest) ||
		!digestPattern.MatchString(i.target.OwnershipProofDigest) || i.nextAttemptAt.IsZero() || i.createdAt.IsZero() ||
		i.updatedAt.Before(i.createdAt) || (i.lastErrorCode != "" && !boundedName(i.lastErrorCode, 128)) {
		return false
	}
	switch i.state {
	case Pending:
		return i.confirmedAt == nil && i.quarantinedAt == nil
	case Confirmed:
		return i.attempts > 0 && i.confirmedAt != nil && i.confirmedAt.Equal(i.updatedAt) && i.quarantinedAt == nil && i.lastErrorCode == ""
	case Quarantined:
		return i.attempts > 0 && i.quarantinedAt != nil && i.quarantinedAt.Equal(i.updatedAt) && i.confirmedAt == nil && i.lastErrorCode != ""
	default:
		return false
	}
}

func validID(v primitives.ID) bool { _, err := primitives.ParseID(string(v)); return err == nil }
func bounded(v string, max int) bool {
	_, err := primitives.BoundedString(v, 1, max, max)
	return err == nil
}
func boundedName(v string, max int) bool { return len(v) <= max && namePattern.MatchString(v) }
func copyTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	x := v.UTC()
	return &x
}

func (i *Intent) TenantID() primitives.ID  { return i.tenantID }
func (i *Intent) ID() primitives.ID        { return i.id }
func (i *Intent) Target() Target           { return i.target }
func (i *Intent) State() State             { return i.state }
func (i *Intent) Version() uint64          { return i.version }
func (i *Intent) Attempts() uint64         { return i.attempts }
func (i *Intent) NextAttemptAt() time.Time { return i.nextAttemptAt }
func (i *Intent) LastErrorCode() string    { return i.lastErrorCode }
func (i *Intent) CreatedAt() time.Time     { return i.createdAt }
func (i *Intent) UpdatedAt() time.Time     { return i.updatedAt }
func (i *Intent) ConfirmedAt() (time.Time, bool) {
	if i.confirmedAt == nil {
		return time.Time{}, false
	}
	return *i.confirmedAt, true
}
func (i *Intent) QuarantinedAt() (time.Time, bool) {
	if i.quarantinedAt == nil {
		return time.Time{}, false
	}
	return *i.quarantinedAt, true
}
