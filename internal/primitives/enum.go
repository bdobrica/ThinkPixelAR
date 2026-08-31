package primitives

import "errors"

var ErrInvalidEnum = errors.New("invalid enum value")

// ParseEnum accepts only an exact member of a closed enum. It deliberately
// does not trim whitespace or fold case.
func ParseEnum[T ~string](value string, allowed ...T) (T, error) {
	for _, candidate := range allowed {
		if value == string(candidate) {
			return candidate, nil
		}
	}
	var zero T
	return zero, ErrInvalidEnum
}
