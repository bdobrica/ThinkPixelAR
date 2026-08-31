// Package clock defines the injectable UTC time source used by application code.
package clock

import "time"

// Clock supplies the current time. Implementations must return UTC values.
type Clock interface {
	Now() time.Time
}

// UTC is the production clock.
type UTC struct{}

// Now returns the current UTC time.
func (UTC) Now() time.Time { return time.Now().UTC() }

// Fixed is a deterministic clock intended for tests and replayable workflows.
type Fixed struct {
	Time time.Time
}

// Now returns Time normalized to UTC.
func (c Fixed) Now() time.Time { return c.Time.UTC() }
