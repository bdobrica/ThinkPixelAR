package primitives

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var ErrOutOfBounds = errors.New("value is outside allowed bounds")

// BoundedString validates UTF-8 and byte/rune limits without modifying input.
func BoundedString(value string, minBytes, maxBytes, maxRunes int) (string, error) {
	if minBytes < 0 || maxBytes < minBytes || maxRunes < 0 {
		return "", fmt.Errorf("%w: invalid limits", ErrOutOfBounds)
	}
	if !utf8.ValidString(value) || len(value) < minBytes || len(value) > maxBytes || utf8.RuneCountInString(value) > maxRunes {
		return "", ErrOutOfBounds
	}
	return value, nil
}

// BoundedPayload returns an owned copy when the payload is within its byte limit.
func BoundedPayload(value []byte, maxBytes int) ([]byte, error) {
	if maxBytes < 0 || len(value) > maxBytes {
		return nil, ErrOutOfBounds
	}
	return append([]byte(nil), value...), nil
}
