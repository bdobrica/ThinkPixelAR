package primitives

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundsOwnPayloadAndValidateStrings(t *testing.T) {
	input := []byte("safe")
	got, err := BoundedPayload(input, len(input))
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	if string(got) != "safe" {
		t.Fatalf("payload retained caller storage: %q", got)
	}
	for _, value := range []string{"", "toolong", string([]byte{0xff})} {
		if _, err := BoundedString(value, 1, 4, 4); !errors.Is(err, ErrOutOfBounds) {
			t.Errorf("BoundedString(%q) error = %v", value, err)
		}
	}
}

func TestParseEnumRequiresExactClosedValue(t *testing.T) {
	type state string
	const ready state = "ready"
	if got, err := ParseEnum("ready", ready); err != nil || got != ready {
		t.Fatalf("ParseEnum() = %q, %v", got, err)
	}
	for _, value := range []string{"READY", " ready", "ready "} {
		if _, err := ParseEnum(value, ready); !errors.Is(err, ErrInvalidEnum) {
			t.Errorf("ParseEnum(%q) error = %v", value, err)
		}
	}
}

func TestTypedErrorDoesNotExposeCause(t *testing.T) {
	secret := errors.New("provider bearer secret-canary")
	typed, err := NewError(Unavailable, "dependency-timeout", "dependency timed out", secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(typed.Error(), "secret-canary") || !errors.Is(typed, secret) {
		t.Fatalf("typed error has unsafe display or lost internal cause: %q", typed)
	}
	if typed.Kind() != Unavailable || typed.Code() != "dependency-timeout" || typed.SafeMessage() != "dependency timed out" {
		t.Fatalf("typed error accessors returned unexpected values")
	}
	if _, err := NewError(ErrorKind("invented"), "bad_code", "safe", nil); err == nil {
		t.Fatal("NewError accepted an open kind and invalid code")
	}
}
