// Package reconciliation models durable, fenced reconciler coordination.
package reconciliation

import (
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type State string

const (
	Pending   State = "PENDING"
	Claimed   State = "CLAIMED"
	Completed State = "COMPLETED"
)

var (
	ErrInvalidWork   = errors.New("invalid reconciliation work")
	ErrClaimConflict = errors.New("reconciliation claim conflict")
	namePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
)

// Work is a stable reconciliation operation. Its lease coordinates workers
// only; callers must still apply aggregate and authority fences when mutating.
type Work struct {
	tenantID, id, targetID, ownerID primitives.ID
	kind, targetType, lastErrorCode string
	state                           State
	attempts, claimFence            uint64
	nextAttemptAt                   time.Time
	claimExpiresAt, completedAt     *time.Time
	createdAt, updatedAt            time.Time
}

func New(tenantID, id primitives.ID, kind, targetType string, targetID primitives.ID, nextAttemptAt, now time.Time) (*Work, error) {
	w := &Work{tenantID: tenantID, id: id, kind: kind, targetType: targetType, targetID: targetID,
		state: Pending, nextAttemptAt: nextAttemptAt.UTC(), createdAt: now.UTC(), updatedAt: now.UTC()}
	if !w.valid() || now.IsZero() || nextAttemptAt.IsZero() {
		return nil, ErrInvalidWork
	}
	return w, nil
}

func Restore(tenantID, id primitives.ID, kind, targetType string, targetID primitives.ID, state State,
	attempts, claimFence uint64, ownerID primitives.ID, nextAttemptAt time.Time, claimExpiresAt *time.Time,
	lastErrorCode string, createdAt, updatedAt time.Time, completedAt *time.Time) (*Work, error) {
	w := &Work{tenantID: tenantID, id: id, kind: kind, targetType: targetType, targetID: targetID, state: state,
		attempts: attempts, claimFence: claimFence, ownerID: ownerID, nextAttemptAt: nextAttemptAt.UTC(),
		claimExpiresAt: copyTime(claimExpiresAt), lastErrorCode: lastErrorCode, createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(), completedAt: copyTime(completedAt)}
	if !w.valid() {
		return nil, ErrInvalidWork
	}
	return w, nil
}

func (w *Work) Claim(ownerID primitives.ID, leaseExpiresAt, now time.Time) error {
	if w == nil || !validID(ownerID) || now.IsZero() || !leaseExpiresAt.After(now) || now.UTC().Before(w.nextAttemptAt) ||
		(w.state != Pending && (w.state != Claimed || w.claimExpiresAt == nil || now.UTC().Before(*w.claimExpiresAt))) {
		return ErrClaimConflict
	}
	w.state, w.ownerID = Claimed, ownerID
	w.attempts++
	w.claimFence++
	lease := leaseExpiresAt.UTC()
	w.claimExpiresAt, w.updatedAt = &lease, now.UTC()
	return nil
}

func (w *Work) Renew(ownerID primitives.ID, fence uint64, leaseExpiresAt, now time.Time) error {
	if !w.owns(ownerID, fence, now) || !leaseExpiresAt.After(*w.claimExpiresAt) {
		return ErrClaimConflict
	}
	lease := leaseExpiresAt.UTC()
	w.claimExpiresAt, w.updatedAt = &lease, now.UTC()
	return nil
}

func (w *Work) Reschedule(ownerID primitives.ID, fence uint64, nextAttemptAt time.Time, errorCode string, now time.Time) error {
	if !w.owns(ownerID, fence, now) || !nextAttemptAt.After(now) || !boundedName(errorCode, 128) {
		return ErrClaimConflict
	}
	w.state, w.ownerID, w.claimExpiresAt = Pending, "", nil
	w.nextAttemptAt, w.lastErrorCode, w.updatedAt = nextAttemptAt.UTC(), errorCode, now.UTC()
	return nil
}

func (w *Work) Complete(ownerID primitives.ID, fence uint64, now time.Time) error {
	if !w.owns(ownerID, fence, now) {
		return ErrClaimConflict
	}
	w.state, w.ownerID, w.claimExpiresAt = Completed, "", nil
	w.updatedAt = now.UTC()
	at := w.updatedAt
	w.completedAt = &at
	return nil
}

func (w *Work) owns(ownerID primitives.ID, fence uint64, now time.Time) bool {
	return w != nil && w.state == Claimed && w.ownerID == ownerID && w.claimFence == fence && !now.IsZero() &&
		w.claimExpiresAt != nil && !now.UTC().After(*w.claimExpiresAt) && !now.UTC().Before(w.updatedAt)
}
func (w *Work) valid() bool {
	if !validID(w.tenantID) || !validID(w.id) || !validID(w.targetID) || !boundedName(w.kind, 128) ||
		!boundedName(w.targetType, 64) || w.nextAttemptAt.IsZero() || w.createdAt.IsZero() ||
		w.updatedAt.Before(w.createdAt) || (w.lastErrorCode != "" && !boundedName(w.lastErrorCode, 128)) {
		return false
	}
	switch w.state {
	case Pending:
		return w.ownerID == "" && w.claimExpiresAt == nil && w.completedAt == nil
	case Claimed:
		return validID(w.ownerID) && w.attempts > 0 && w.claimFence > 0 && w.claimExpiresAt != nil && w.claimExpiresAt.After(w.updatedAt) && w.completedAt == nil
	case Completed:
		return w.ownerID == "" && w.claimExpiresAt == nil && w.attempts > 0 && w.claimFence > 0 &&
			w.completedAt != nil && w.completedAt.Equal(w.updatedAt)
	default:
		return false
	}
}
func validID(v primitives.ID) bool       { _, err := primitives.ParseID(string(v)); return err == nil }
func boundedName(v string, max int) bool { return len(v) <= max && namePattern.MatchString(v) }
func copyTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	x := v.UTC()
	return &x
}

func (w *Work) TenantID() primitives.ID  { return w.tenantID }
func (w *Work) ID() primitives.ID        { return w.id }
func (w *Work) Kind() string             { return w.kind }
func (w *Work) TargetType() string       { return w.targetType }
func (w *Work) TargetID() primitives.ID  { return w.targetID }
func (w *Work) State() State             { return w.state }
func (w *Work) Attempts() uint64         { return w.attempts }
func (w *Work) ClaimFence() uint64       { return w.claimFence }
func (w *Work) OwnerID() primitives.ID   { return w.ownerID }
func (w *Work) NextAttemptAt() time.Time { return w.nextAttemptAt }
func (w *Work) ClaimExpiresAt() (time.Time, bool) {
	if w.claimExpiresAt == nil {
		return time.Time{}, false
	}
	return *w.claimExpiresAt, true
}
func (w *Work) LastErrorCode() string { return w.lastErrorCode }
func (w *Work) CreatedAt() time.Time  { return w.createdAt }
func (w *Work) UpdatedAt() time.Time  { return w.updatedAt }
func (w *Work) CompletedAt() (time.Time, bool) {
	if w.completedAt == nil {
		return time.Time{}, false
	}
	return *w.completedAt, true
}
