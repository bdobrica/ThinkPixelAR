package primitives

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
)

const maxCursorPayload = 4096

var ErrInvalidCursor = errors.New("invalid cursor")

// Cursor is the authenticated pagination context. It conveys no authorization;
// callers must still authorize every returned resource.
type Cursor struct {
	TenantID        string    `json:"tenant_id"`
	PrincipalID     string    `json:"principal_id"`
	SecurityEpoch   uint64    `json:"security_epoch"`
	Route           string    `json:"route"`
	APIVersion      string    `json:"api_version"`
	QueryDigest     string    `json:"query_digest"`
	LastKey         string    `json:"last_key"`
	PageSizeCeiling int       `json:"page_size_ceiling"`
	Snapshot        string    `json:"snapshot,omitempty"`
	Retention       string    `json:"retention,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// CursorContext is the request context that a decoded cursor must match.
type CursorContext struct {
	TenantID      string
	PrincipalID   string
	SecurityEpoch uint64
	Route         string
	APIVersion    string
	QueryDigest   string
}

// CursorCodec encodes MAC-authenticated, versioned cursor tokens.
type CursorCodec struct {
	key   []byte
	clock clock.Clock
}

func NewCursorCodec(key []byte, c clock.Clock) (*CursorCodec, error) {
	if len(key) < 32 || c == nil {
		return nil, errors.New("cursor requires a clock and at least 32 key bytes")
	}
	return &CursorCodec{key: append([]byte(nil), key...), clock: c}, nil
}

func (c *CursorCodec) Encode(value Cursor) (string, error) {
	if err := validateCursor(value, c.clock.Now()); err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > maxCursorPayload {
		return "", ErrInvalidCursor
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := c.sign(encoded)
	return "v1." + encoded + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *CursorCodec) Decode(token string) (Cursor, error) {
	var value Cursor
	if len(token) > 8192 {
		return value, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return value, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, c.sign(parts[1])) {
		return value, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > maxCursorPayload {
		return value, ErrInvalidCursor
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Cursor{}, ErrInvalidCursor
	}
	if err := validateCursor(value, c.clock.Now()); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return value, nil
}

// DecodeFor decodes a cursor and rejects substitution into a different
// tenant, principal visibility epoch, route, API version, filter, or sort.
func (c *CursorCodec) DecodeFor(token string, expected CursorContext) (Cursor, error) {
	value, err := c.Decode(token)
	if err != nil {
		return Cursor{}, err
	}
	if value.TenantID != expected.TenantID || value.PrincipalID != expected.PrincipalID ||
		value.SecurityEpoch != expected.SecurityEpoch || value.Route != expected.Route ||
		value.APIVersion != expected.APIVersion || value.QueryDigest != expected.QueryDigest {
		return Cursor{}, ErrInvalidCursor
	}
	return value, nil
}

func (c *CursorCodec) sign(payload string) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte("v1." + payload))
	return mac.Sum(nil)
}

func validateCursor(value Cursor, now time.Time) error {
	fields := []string{value.TenantID, value.PrincipalID, value.Route, value.APIVersion, value.QueryDigest, value.LastKey}
	for _, field := range fields {
		if _, err := BoundedString(field, 1, 512, 512); err != nil {
			return ErrInvalidCursor
		}
	}
	for _, field := range []string{value.Snapshot, value.Retention} {
		if _, err := BoundedString(field, 0, 512, 512); err != nil {
			return ErrInvalidCursor
		}
	}
	if value.PageSizeCeiling < 1 || value.PageSizeCeiling > 200 || value.ExpiresAt.Location() != time.UTC || !value.ExpiresAt.After(now.UTC()) {
		return fmt.Errorf("%w: invalid bounds or expiry", ErrInvalidCursor)
	}
	return nil
}
