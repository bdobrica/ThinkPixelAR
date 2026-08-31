// Package telemetry provides runtime observability plumbing.
//
// Logging in this package is intentionally sink-oriented: records are redacted
// before they reach the wrapped slog handler. Callers should still construct
// records from a small, reviewed set of fields rather than logging request,
// configuration, or provider objects wholesale.
package telemetry
