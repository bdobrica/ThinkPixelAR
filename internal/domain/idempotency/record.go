// Package idempotency models durable, scoped mutation identity and replay state.
package idempotency

import (
	"bytes"
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type State string

const (
	InProgress State = "IN_PROGRESS"
	Succeeded  State = "SUCCEEDED"
	Failed     State = "FAILED"
)

var (
	ErrInvalidRecord  = errors.New("invalid idempotency record")
	ErrOwnership      = errors.New("idempotency ownership conflict")
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
)

type Scope struct {
	PrincipalDigest string
	Action          string
	KeyDigest       string
}

type Response struct {
	HTTPStatus int
	Payload    []byte
	Reference  string
}

type Failure struct {
	HTTPStatus  int
	ProblemType string
	ProblemCode string
}

// Record is one durable logical mutation. The owner fence prevents a stale
// worker from completing an operation after lease takeover.
type Record struct {
	tenantID, id, operationID, resourceID, ownerID, auditCorrelationID primitives.ID
	scope                                                              Scope
	normalizationVersion, requestDigest                                string
	state                                                              State
	ownerFence                                                         uint64
	leaseExpiresAt, completedAt                                        *time.Time
	response                                                           *Response
	failure                                                            *Failure
	createdAt, updatedAt, expiresAt                                    time.Time
}

func New(tenantID, id primitives.ID, scope Scope, normalizationVersion, requestDigest string,
	operationID, resourceID, ownerID, auditCorrelationID primitives.ID, leaseExpiresAt, expiresAt, now time.Time) (*Record, error) {
	if !validIDs(tenantID, id, operationID, ownerID, auditCorrelationID) || (resourceID != "" && !validIDs(resourceID)) ||
		!validScope(scope) || !boundedIdentifier(normalizationVersion, 64) || !digestPattern.MatchString(requestDigest) ||
		now.IsZero() || !leaseExpiresAt.After(now) || !expiresAt.After(leaseExpiresAt) {
		return nil, ErrInvalidRecord
	}
	now, leaseExpiresAt, expiresAt = now.UTC(), leaseExpiresAt.UTC(), expiresAt.UTC()
	return &Record{tenantID: tenantID, id: id, operationID: operationID, resourceID: resourceID, ownerID: ownerID,
		auditCorrelationID: auditCorrelationID, scope: scope, normalizationVersion: normalizationVersion,
		requestDigest: requestDigest, state: InProgress, ownerFence: 1, leaseExpiresAt: &leaseExpiresAt,
		createdAt: now, updatedAt: now, expiresAt: expiresAt}, nil
}

func Restore(tenantID, id primitives.ID, scope Scope, normalizationVersion, requestDigest string,
	operationID, resourceID, ownerID, auditCorrelationID primitives.ID, state State, ownerFence uint64,
	leaseExpiresAt *time.Time, response *Response, failure *Failure, createdAt, updatedAt time.Time,
	completedAt *time.Time, expiresAt time.Time) (*Record, error) {
	r := &Record{tenantID: tenantID, id: id, operationID: operationID, resourceID: resourceID, ownerID: ownerID,
		auditCorrelationID: auditCorrelationID, scope: scope, normalizationVersion: normalizationVersion,
		requestDigest: requestDigest, state: state, ownerFence: ownerFence, leaseExpiresAt: copyTime(leaseExpiresAt),
		response: copyResponse(response), failure: copyFailure(failure), createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC(),
		completedAt: copyTime(completedAt), expiresAt: expiresAt.UTC()}
	if !r.valid() {
		return nil, ErrInvalidRecord
	}
	return r, nil
}

func (r *Record) TakeOwnership(ownerID primitives.ID, expectedFence uint64, leaseExpiresAt, now time.Time) error {
	if r == nil || r.state != InProgress || expectedFence != r.ownerFence || !validIDs(ownerID) || now.IsZero() ||
		r.leaseExpiresAt == nil || now.UTC().Before(*r.leaseExpiresAt) || !leaseExpiresAt.After(now) || !r.expiresAt.After(leaseExpiresAt) {
		return ErrOwnership
	}
	r.ownerID, r.ownerFence = ownerID, r.ownerFence+1
	lease := leaseExpiresAt.UTC()
	r.leaseExpiresAt, r.updatedAt = &lease, now.UTC()
	return nil
}

func (r *Record) Renew(ownerID primitives.ID, fence uint64, leaseExpiresAt, now time.Time) error {
	if r == nil || r.state != InProgress || r.ownerID != ownerID || r.ownerFence != fence || now.IsZero() ||
		r.leaseExpiresAt == nil || now.UTC().After(*r.leaseExpiresAt) || !leaseExpiresAt.After(now) ||
		!leaseExpiresAt.After(*r.leaseExpiresAt) || !r.expiresAt.After(leaseExpiresAt) {
		return ErrOwnership
	}
	lease := leaseExpiresAt.UTC()
	r.leaseExpiresAt, r.updatedAt = &lease, now.UTC()
	return nil
}

func (r *Record) Succeed(response Response, ownerID primitives.ID, fence uint64, now time.Time) error {
	if !r.canComplete(ownerID, fence, now) {
		return ErrOwnership
	}
	if !validResponse(response) {
		return ErrInvalidRecord
	}
	r.state = Succeeded
	r.response = copyResponse(&response)
	r.leaseExpiresAt = nil
	r.updatedAt = now.UTC()
	at := r.updatedAt
	r.completedAt = &at
	return nil
}

func (r *Record) Fail(failure Failure, ownerID primitives.ID, fence uint64, now time.Time) error {
	if !r.canComplete(ownerID, fence, now) {
		return ErrOwnership
	}
	if !validFailure(failure) {
		return ErrInvalidRecord
	}
	r.state = Failed
	r.failure = copyFailure(&failure)
	r.leaseExpiresAt = nil
	r.updatedAt = now.UTC()
	at := r.updatedAt
	r.completedAt = &at
	return nil
}

func (r *Record) canComplete(ownerID primitives.ID, fence uint64, now time.Time) bool {
	return r != nil && r.state == InProgress && r.ownerID == ownerID && r.ownerFence == fence && !now.IsZero() &&
		r.leaseExpiresAt != nil && !now.UTC().After(*r.leaseExpiresAt) && !now.UTC().Before(r.updatedAt)
}

func (r *Record) valid() bool {
	if !validIDs(r.tenantID, r.id, r.operationID, r.ownerID, r.auditCorrelationID) || (r.resourceID != "" && !validIDs(r.resourceID)) ||
		!validScope(r.scope) || !boundedIdentifier(r.normalizationVersion, 64) || !digestPattern.MatchString(r.requestDigest) ||
		r.ownerFence == 0 || r.createdAt.IsZero() || r.updatedAt.Before(r.createdAt) || !r.expiresAt.After(r.createdAt) {
		return false
	}
	switch r.state {
	case InProgress:
		return r.leaseExpiresAt != nil && r.completedAt == nil && r.response == nil && r.failure == nil && r.expiresAt.After(*r.leaseExpiresAt)
	case Succeeded:
		return r.leaseExpiresAt == nil && r.completedAt != nil && r.response != nil && r.failure == nil && validResponse(*r.response)
	case Failed:
		return r.leaseExpiresAt == nil && r.completedAt != nil && r.response == nil && r.failure != nil && validFailure(*r.failure)
	default:
		return false
	}
}

func validScope(s Scope) bool {
	return digestPattern.MatchString(s.PrincipalDigest) && boundedIdentifier(s.Action, 255) && digestPattern.MatchString(s.KeyDigest)
}
func validResponse(v Response) bool {
	return v.HTTPStatus >= 200 && v.HTTPStatus <= 399 && len(v.Payload) <= 65536 && (len(v.Payload) > 0 || v.Reference != "") && boundedOptional(v.Reference, 2048)
}
func validFailure(v Failure) bool {
	return v.HTTPStatus >= 400 && v.HTTPStatus <= 499 && boundedIdentifier(v.ProblemType, 255) && boundedIdentifier(v.ProblemCode, 128)
}
func boundedIdentifier(v string, max int) bool {
	return len(v) <= max && identifierPattern.MatchString(v)
}
func boundedOptional(v string, max int) bool {
	return v == "" || (len(v) <= max && bytes.IndexByte([]byte(v), 0) < 0)
}
func validIDs(ids ...primitives.ID) bool {
	for _, id := range ids {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false
		}
	}
	return true
}
func copyTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	x := v.UTC()
	return &x
}
func copyResponse(v *Response) *Response {
	if v == nil {
		return nil
	}
	return &Response{HTTPStatus: v.HTTPStatus, Payload: append([]byte(nil), v.Payload...), Reference: v.Reference}
}
func copyFailure(v *Failure) *Failure {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func (r *Record) TenantID() primitives.ID           { return r.tenantID }
func (r *Record) ID() primitives.ID                 { return r.id }
func (r *Record) Scope() Scope                      { return r.scope }
func (r *Record) NormalizationVersion() string      { return r.normalizationVersion }
func (r *Record) RequestDigest() string             { return r.requestDigest }
func (r *Record) OperationID() primitives.ID        { return r.operationID }
func (r *Record) ResourceID() primitives.ID         { return r.resourceID }
func (r *Record) OwnerID() primitives.ID            { return r.ownerID }
func (r *Record) OwnerFence() uint64                { return r.ownerFence }
func (r *Record) AuditCorrelationID() primitives.ID { return r.auditCorrelationID }
func (r *Record) State() State                      { return r.state }
func (r *Record) LeaseExpiresAt() (time.Time, bool) {
	if r.leaseExpiresAt == nil {
		return time.Time{}, false
	}
	return *r.leaseExpiresAt, true
}
func (r *Record) Response() (Response, bool) {
	if r.response == nil {
		return Response{}, false
	}
	return *copyResponse(r.response), true
}
func (r *Record) Failure() (Failure, bool) {
	if r.failure == nil {
		return Failure{}, false
	}
	return *r.failure, true
}
func (r *Record) CreatedAt() time.Time { return r.createdAt }
func (r *Record) UpdatedAt() time.Time { return r.updatedAt }
func (r *Record) CompletedAt() (time.Time, bool) {
	if r.completedAt == nil {
		return time.Time{}, false
	}
	return *r.completedAt, true
}
func (r *Record) ExpiresAt() time.Time { return r.expiresAt }
