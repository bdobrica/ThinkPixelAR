package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const (
	RequestIDKey   = "request_id"
	TraceIDKey     = "trace_id"
	SessionIDKey   = "session_id"
	ExecutionIDKey = "execution_id"
	AttemptIDKey   = "attempt_id"

	redactedValue = "[REDACTED]"
	maxValueBytes = 4096
	maxDepth      = 16
	maxCollection = 128
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\b(?:gh[pousr]|github_pat)_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
}

// Correlation contains the canonical high-cardinality identifiers permitted in
// logs and traces. It must not be reused as a metric label set.
type Correlation struct {
	RequestID   string
	TraceID     string
	SessionID   string
	ExecutionID string
	AttemptID   string
}

// WithCorrelation returns a logger carrying each non-empty canonical field.
func WithCorrelation(logger *slog.Logger, correlation Correlation) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	attributes := make([]any, 0, 10)
	appendField := func(key, value string) {
		if value != "" {
			attributes = append(attributes, key, value)
		}
	}
	appendField(RequestIDKey, correlation.RequestID)
	appendField(TraceIDKey, correlation.TraceID)
	appendField(SessionIDKey, correlation.SessionID)
	appendField(ExecutionIDKey, correlation.ExecutionID)
	appendField(AttemptIDKey, correlation.AttemptID)
	return logger.With(attributes...)
}

// LogOptions configures a JSON logger. Secrets are exact values already known
// to the trusted process and are removed even when logged under an unusual key.
type LogOptions struct {
	Level   slog.Leveler
	Secrets []string
}

// NewJSONLogger creates a structured logger whose records are recursively
// redacted before JSON serialization.
func NewJSONLogger(writer io.Writer, options LogOptions) *slog.Logger {
	return slog.New(NewRedactingHandler(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: options.Level,
	}), options.Secrets...))
}

// NewRedactingHandler wraps a sink handler with recursive key, exact-value,
// token-pattern, URL, error, and length redaction.
func NewRedactingHandler(next slog.Handler, secrets ...string) slog.Handler {
	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			filtered = append(filtered, secret)
		}
	}
	return &redactingHandler{next: next, secrets: filtered}
}

type redactingHandler struct {
	next    slog.Handler
	secrets []string
}

func (handler *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, handler.cleanText(record.Message), record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		clean.AddAttrs(handler.cleanAttr(attribute, 0))
		return true
	})
	return handler.next.Handle(ctx, clean)
}

func (handler *redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		clean = append(clean, handler.cleanAttr(attribute, 0))
	}
	return &redactingHandler{next: handler.next.WithAttrs(clean), secrets: handler.secrets}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: handler.next.WithGroup(name), secrets: handler.secrets}
}

func (handler *redactingHandler) cleanAttr(attribute slog.Attr, depth int) slog.Attr {
	if sensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, redactedValue)
	}
	value := attribute.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		if depth >= maxDepth {
			return slog.String(attribute.Key, "[TRUNCATED]")
		}
		group := value.Group()
		clean := make([]slog.Attr, 0, min(len(group), maxCollection))
		for index, child := range group {
			if index == maxCollection {
				break
			}
			clean = append(clean, handler.cleanAttr(child, depth+1))
		}
		return slog.Group(attribute.Key, attrsToAny(clean)...)
	}
	return slog.Any(attribute.Key, handler.cleanValue(value, depth))
}

func (handler *redactingHandler) cleanValue(value slog.Value, depth int) any {
	switch value.Kind() {
	case slog.KindString:
		return handler.cleanText(value.String())
	case slog.KindTime:
		return value.Time()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindBool:
		return value.Bool()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindAny:
		return handler.cleanAny(reflect.ValueOf(value.Any()), depth, make(map[visit]bool))
	default:
		return handler.cleanText(value.String())
	}
}

type visit struct {
	typeName reflect.Type
	pointer  uintptr
}

func (handler *redactingHandler) cleanAny(value reflect.Value, depth int, seen map[visit]bool) any {
	if !value.IsValid() {
		return nil
	}
	if depth >= maxDepth {
		return "[TRUNCATED]"
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return handler.cleanAny(value.Elem(), depth+1, seen)
	}
	if value.CanInterface() {
		switch item := value.Interface().(type) {
		case error:
			return handler.cleanText(item.Error())
		case *url.URL:
			return handler.cleanURL(item)
		case url.URL:
			copy := item
			return handler.cleanURL(&copy)
		case time.Time:
			return item
		case time.Duration:
			return item
		case fmt.Stringer:
			return handler.cleanText(item.String())
		}
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		key := visit{value.Type(), value.Pointer()}
		if seen[key] {
			return "[CYCLE]"
		}
		seen[key] = true
		defer delete(seen, key)
		return handler.cleanAny(value.Elem(), depth+1, seen)
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		result := make(map[string]any)
		iterator := value.MapRange()
		for len(result) < maxCollection && iterator.Next() {
			key := handler.cleanText(fmt.Sprint(iterator.Key().Interface()))
			if sensitiveKey(key) {
				result[key] = redactedValue
			} else {
				result[key] = handler.cleanAny(iterator.Value(), depth+1, seen)
			}
		}
		return result
	case reflect.Struct:
		result := make(map[string]any)
		typeInfo := value.Type()
		for index := 0; index < value.NumField() && len(result) < maxCollection; index++ {
			field := typeInfo.Field(index)
			if field.PkgPath != "" { // unexported
				continue
			}
			name := field.Name
			if tag := strings.Split(field.Tag.Get("json"), ",")[0]; tag != "" {
				if tag == "-" {
					continue
				}
				name = tag
			}
			if sensitiveKey(name) {
				result[name] = redactedValue
			} else {
				result[name] = handler.cleanAny(value.Field(index), depth+1, seen)
			}
		}
		return result
	case reflect.Slice, reflect.Array:
		length := min(value.Len(), maxCollection)
		result := make([]any, 0, length)
		for index := 0; index < length; index++ {
			result = append(result, handler.cleanAny(value.Index(index), depth+1, seen))
		}
		return result
	case reflect.String:
		return handler.cleanText(value.String())
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		return value.Float()
	default:
		if value.CanInterface() {
			return handler.cleanText(fmt.Sprint(value.Interface()))
		}
		return "[UNSUPPORTED]"
	}
}

func (handler *redactingHandler) cleanURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	copy := *value
	if copy.User != nil {
		copy.User = url.User(redactedValue)
	}
	query := copy.Query()
	for key := range query {
		if sensitiveKey(key) {
			query.Set(key, redactedValue)
		} else {
			for index, item := range query[key] {
				query[key][index] = handler.cleanText(item)
			}
		}
	}
	copy.RawQuery = query.Encode()
	return handler.cleanText(copy.String())
}

func (handler *redactingHandler) cleanText(value string) string {
	for _, secret := range handler.secrets {
		value = strings.ReplaceAll(value, secret, redactedValue)
	}
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() && (parsed.User != nil || parsed.RawQuery != "") {
		value = handler.cleanURLWithoutRecursion(parsed)
	}
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, redactedValue)
	}
	if len(value) > maxValueBytes {
		value = value[:maxValueBytes] + "[TRUNCATED]"
	}
	return value
}

func (handler *redactingHandler) cleanURLWithoutRecursion(value *url.URL) string {
	copy := *value
	if copy.User != nil {
		copy.User = url.User(redactedValue)
	}
	query := copy.Query()
	for key := range query {
		if sensitiveKey(key) {
			query.Set(key, redactedValue)
		} else {
			for index, item := range query[key] {
				for _, secret := range handler.secrets {
					item = strings.ReplaceAll(item, secret, redactedValue)
				}
				query[key][index] = item
			}
		}
	}
	copy.RawQuery = query.Encode()
	return copy.String()
}

func sensitiveKey(key string) bool {
	normalized := strings.Map(func(character rune) rune {
		if character >= 'A' && character <= 'Z' {
			return character + ('a' - 'A')
		}
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		return -1
	}, key)
	for _, marker := range []string{
		"authorization", "password", "passwd", "privatekey", "clientsecret",
		"accesstoken", "refreshtoken", "idtoken", "apikey", "accesskey",
		"credential", "cookie", "token", "secret",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func attrsToAny(attributes []slog.Attr) []any {
	result := make([]any, len(attributes))
	for index := range attributes {
		result[index] = attributes[index]
	}
	return result
}
