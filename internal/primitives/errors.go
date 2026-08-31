package primitives

import (
	"errors"
	"fmt"
)

// ErrorKind is a stable, closed error classification used before transport mapping.
type ErrorKind string

const (
	Invalid     ErrorKind = "invalid"
	NotFound    ErrorKind = "not_found"
	Conflict    ErrorKind = "conflict"
	Forbidden   ErrorKind = "forbidden"
	Unavailable ErrorKind = "unavailable"
	Internal    ErrorKind = "internal"
)

var errorKinds = []ErrorKind{Invalid, NotFound, Conflict, Forbidden, Unavailable, Internal}

// Error carries a stable kind/code and a bounded safe message. Cause remains
// available for internal matching but is never included by Error().
type Error struct {
	kind    ErrorKind
	code    string
	message string
	cause   error
}

// NewError validates and constructs a typed error.
func NewError(kind ErrorKind, code, safeMessage string, cause error) (*Error, error) {
	if _, err := ParseEnum(string(kind), errorKinds...); err != nil {
		return nil, fmt.Errorf("invalid error kind: %w", err)
	}
	if !validCode(code) {
		return nil, errors.New("invalid error code")
	}
	if _, err := BoundedString(safeMessage, 1, 256, 256); err != nil {
		return nil, errors.New("invalid safe error message")
	}
	return &Error{kind: kind, code: code, message: safeMessage, cause: cause}, nil
}

func (e *Error) Error() string { return e.code + ": " + e.message }
func (e *Error) Unwrap() error { return e.cause }

// Kind returns the stable internal error classification.
func (e *Error) Kind() ErrorKind { return e.kind }

// Code returns the stable machine-readable error code.
func (e *Error) Code() string { return e.code }

// SafeMessage returns the bounded message approved for boundary mapping.
func (e *Error) SafeMessage() string { return e.message }

func validCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(value)-1) {
			continue
		}
		return false
	}
	return true
}
