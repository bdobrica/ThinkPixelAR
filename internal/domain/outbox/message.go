// Package outbox models durable, replay-safe publication intent.
package outbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

const MaxPayloadBytes = 64 * 1024

type State string

const (
	Pending      State = "PENDING"
	Claimed      State = "CLAIMED"
	Delivered    State = "DELIVERED"
	DeadLettered State = "DEAD_LETTERED"
)

var (
	ErrInvalidMessage = errors.New("invalid outbox message")
	ErrClaimConflict  = errors.New("outbox claim conflict")
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	namePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
)

type Envelope struct {
	Topic            string
	SchemaVersion    string
	EventID          primitives.ID
	AggregateType    string
	AggregateID      primitives.ID
	AggregateVersion uint64
	Payload          []byte
	PayloadReference string
	PayloadDigest    string
}

type DeadLetter struct {
	ReasonCode string
	Detail     string
}

// Message is a semantic publication identity. Claims coordinate dispatch but
// do not change the identity consumers use for deduplication.
type Message struct {
	tenantID, id, ownerID primitives.ID
	envelope              Envelope
	state                 State
	attempts, claimFence  uint64
	availableAt           time.Time
	claimExpiresAt        *time.Time
	lastErrorCode         string
	deadLetter            *DeadLetter
	createdAt, updatedAt  time.Time
	deliveredAt           *time.Time
}

func New(tenantID, id primitives.ID, envelope Envelope, availableAt, now time.Time) (*Message, error) {
	m := &Message{tenantID: tenantID, id: id, envelope: copyEnvelope(envelope), state: Pending,
		availableAt: availableAt.UTC(), createdAt: now.UTC(), updatedAt: now.UTC()}
	if !m.valid() || availableAt.IsZero() || now.IsZero() {
		return nil, ErrInvalidMessage
	}
	return m, nil
}

func Restore(tenantID, id primitives.ID, envelope Envelope, state State, attempts, claimFence uint64,
	ownerID primitives.ID, availableAt time.Time, claimExpiresAt *time.Time, lastErrorCode string,
	deadLetter *DeadLetter, createdAt, updatedAt time.Time, deliveredAt *time.Time) (*Message, error) {
	m := &Message{tenantID: tenantID, id: id, ownerID: ownerID, envelope: copyEnvelope(envelope), state: state,
		attempts: attempts, claimFence: claimFence, availableAt: availableAt.UTC(), claimExpiresAt: copyTime(claimExpiresAt),
		lastErrorCode: lastErrorCode, deadLetter: copyDeadLetter(deadLetter), createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(), deliveredAt: copyTime(deliveredAt)}
	if !m.valid() {
		return nil, ErrInvalidMessage
	}
	return m, nil
}

func (m *Message) Claim(ownerID primitives.ID, leaseExpiresAt, now time.Time) error {
	if m == nil || !validID(ownerID) || now.IsZero() || !leaseExpiresAt.After(now) || now.UTC().Before(m.availableAt) ||
		(m.state != Pending && (m.state != Claimed || m.claimExpiresAt == nil || now.UTC().Before(*m.claimExpiresAt))) {
		return ErrClaimConflict
	}
	m.state, m.ownerID = Claimed, ownerID
	m.attempts++
	m.claimFence++
	lease := leaseExpiresAt.UTC()
	m.claimExpiresAt, m.updatedAt = &lease, now.UTC()
	return nil
}

func (m *Message) Retry(ownerID primitives.ID, fence uint64, availableAt time.Time, errorCode string, now time.Time) error {
	if !m.ownsClaim(ownerID, fence, now) || !availableAt.After(now) || !boundedName(errorCode, 128) {
		return ErrClaimConflict
	}
	m.state, m.ownerID, m.claimExpiresAt = Pending, "", nil
	m.availableAt, m.lastErrorCode, m.updatedAt = availableAt.UTC(), errorCode, now.UTC()
	return nil
}

func (m *Message) MarkDelivered(ownerID primitives.ID, fence uint64, now time.Time) error {
	if !m.ownsClaim(ownerID, fence, now) {
		return ErrClaimConflict
	}
	m.state, m.ownerID, m.claimExpiresAt = Delivered, "", nil
	m.updatedAt = now.UTC()
	at := m.updatedAt
	m.deliveredAt = &at
	return nil
}

func (m *Message) DeadLetter(ownerID primitives.ID, fence uint64, metadata DeadLetter, now time.Time) error {
	if !m.ownsClaim(ownerID, fence, now) || !validDeadLetter(metadata) {
		return ErrClaimConflict
	}
	m.state, m.ownerID, m.claimExpiresAt = DeadLettered, "", nil
	m.deadLetter, m.updatedAt = copyDeadLetter(&metadata), now.UTC()
	return nil
}

func (m *Message) ownsClaim(ownerID primitives.ID, fence uint64, now time.Time) bool {
	return m != nil && m.state == Claimed && m.ownerID == ownerID && m.claimFence == fence && !now.IsZero() &&
		m.claimExpiresAt != nil && !now.UTC().After(*m.claimExpiresAt) && !now.UTC().Before(m.updatedAt)
}

func (m *Message) valid() bool {
	if !validID(m.tenantID) || !validID(m.id) || !validEnvelope(m.envelope) || m.availableAt.IsZero() ||
		m.createdAt.IsZero() || m.updatedAt.Before(m.createdAt) || !boundedOptionalName(m.lastErrorCode, 128) {
		return false
	}
	switch m.state {
	case Pending:
		return m.ownerID == "" && m.claimExpiresAt == nil && m.deadLetter == nil && m.deliveredAt == nil
	case Claimed:
		return validID(m.ownerID) && m.attempts > 0 && m.claimFence > 0 && m.claimExpiresAt != nil &&
			m.claimExpiresAt.After(m.updatedAt) && m.deadLetter == nil && m.deliveredAt == nil
	case Delivered:
		return m.ownerID == "" && m.claimExpiresAt == nil && m.deliveredAt != nil && m.deadLetter == nil
	case DeadLettered:
		return m.ownerID == "" && m.claimExpiresAt == nil && m.deliveredAt == nil && m.deadLetter != nil && validDeadLetter(*m.deadLetter)
	default:
		return false
	}
}

func validEnvelope(e Envelope) bool {
	if !boundedName(e.Topic, 255) || !boundedName(e.SchemaVersion, 128) || !validID(e.EventID) ||
		!boundedName(e.AggregateType, 64) || !validID(e.AggregateID) || e.AggregateVersion == 0 ||
		!digestPattern.MatchString(e.PayloadDigest) || len(e.Payload) > MaxPayloadBytes || len(e.PayloadReference) > 2048 ||
		(len(e.Payload) == 0) == (e.PayloadReference == "") {
		return false
	}
	if len(e.Payload) > 0 {
		return json.Valid(e.Payload) && bytes.IndexByte(e.Payload, 0) < 0
	}
	return bytes.IndexByte([]byte(e.PayloadReference), 0) < 0
}

func validDeadLetter(v DeadLetter) bool {
	return boundedName(v.ReasonCode, 128) && len(v.Detail) <= 1024 && bytes.IndexByte([]byte(v.Detail), 0) < 0
}
func boundedName(v string, max int) bool         { return len(v) <= max && namePattern.MatchString(v) }
func boundedOptionalName(v string, max int) bool { return v == "" || boundedName(v, max) }
func validID(v primitives.ID) bool               { _, err := primitives.ParseID(string(v)); return err == nil }
func copyEnvelope(v Envelope) Envelope           { v.Payload = append([]byte(nil), v.Payload...); return v }
func copyDeadLetter(v *DeadLetter) *DeadLetter {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}
func copyTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	x := v.UTC()
	return &x
}

func (m *Message) TenantID() primitives.ID { return m.tenantID }
func (m *Message) ID() primitives.ID       { return m.id }
func (m *Message) Envelope() Envelope      { return copyEnvelope(m.envelope) }
func (m *Message) State() State            { return m.state }
func (m *Message) Attempts() uint64        { return m.attempts }
func (m *Message) ClaimFence() uint64      { return m.claimFence }
func (m *Message) OwnerID() primitives.ID  { return m.ownerID }
func (m *Message) AvailableAt() time.Time  { return m.availableAt }
func (m *Message) ClaimExpiresAt() (time.Time, bool) {
	if m.claimExpiresAt == nil {
		return time.Time{}, false
	}
	return *m.claimExpiresAt, true
}
func (m *Message) LastErrorCode() string { return m.lastErrorCode }
func (m *Message) DeadLetterMetadata() (DeadLetter, bool) {
	if m.deadLetter == nil {
		return DeadLetter{}, false
	}
	return *m.deadLetter, true
}
func (m *Message) CreatedAt() time.Time { return m.createdAt }
func (m *Message) UpdatedAt() time.Time { return m.updatedAt }
func (m *Message) DeliveredAt() (time.Time, bool) {
	if m.deliveredAt == nil {
		return time.Time{}, false
	}
	return *m.deliveredAt, true
}
