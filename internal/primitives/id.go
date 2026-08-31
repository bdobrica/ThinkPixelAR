package primitives

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// ID is a validated canonical UUIDv7 identifier.
type ID string

var ErrInvalidID = errors.New("invalid UUIDv7 identifier")

// NewID creates a UUIDv7 using the supplied UTC clock and cryptographic random source.
func NewID(now time.Time) (ID, error) { return NewIDFrom(now, rand.Reader) }

// NewIDFrom is NewID with an injectable entropy source for deterministic tests.
func NewIDFrom(now time.Time, random io.Reader) (ID, error) {
	if random == nil {
		return "", fmt.Errorf("%w: random source is required", ErrInvalidID)
	}
	millis := now.UTC().UnixMilli()
	if millis < 0 || millis > 1<<48-1 {
		return "", fmt.Errorf("%w: timestamp is out of range", ErrInvalidID)
	}
	var raw [16]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 entropy: %w", err)
	}
	raw[0] = byte(millis >> 40)
	raw[1] = byte(millis >> 32)
	raw[2] = byte(millis >> 24)
	raw[3] = byte(millis >> 16)
	raw[4] = byte(millis >> 8)
	raw[5] = byte(millis)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	return formatID(raw), nil
}

// ParseID accepts only the canonical lowercase UUIDv7 representation.
func ParseID(value string) (ID, error) {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", ErrInvalidID
	}
	compact := value[0:8] + value[9:13] + value[14:18] + value[19:23] + value[24:36]
	var raw [16]byte
	if _, err := hex.Decode(raw[:], []byte(compact)); err != nil || formatID(raw) != ID(value) {
		return "", ErrInvalidID
	}
	if raw[6]>>4 != 7 || raw[8]>>6 != 2 {
		return "", ErrInvalidID
	}
	return ID(value), nil
}

func formatID(raw [16]byte) ID {
	var out [36]byte
	hex.Encode(out[0:8], raw[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], raw[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], raw[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], raw[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], raw[10:16])
	return ID(out[:])
}
