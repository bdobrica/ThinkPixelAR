package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestJSONLoggerAddsCanonicalCorrelationFields(t *testing.T) {
	var output bytes.Buffer
	logger := WithCorrelation(NewJSONLogger(&output, LogOptions{}), Correlation{
		RequestID: "request-1", TraceID: "0123456789abcdef0123456789abcdef",
		SessionID: "session-1", ExecutionID: "execution-1", AttemptID: "attempt-1",
	})
	logger.Info("started")

	record := decodeRecord(t, output.Bytes())
	for key, expected := range map[string]string{
		RequestIDKey: "request-1", TraceIDKey: "0123456789abcdef0123456789abcdef",
		SessionIDKey: "session-1", ExecutionIDKey: "execution-1", AttemptIDKey: "attempt-1",
	} {
		if record[key] != expected {
			t.Errorf("%s = %v, want %q", key, record[key], expected)
		}
	}
}

func TestJSONLoggerRecursivelyRedacts(t *testing.T) {
	type nested struct {
		Name       string            `json:"name"`
		PrivateKey string            `json:"private_key"`
		Values     []map[string]any  `json:"values"`
		Metadata   map[string]string `json:"metadata"`
	}
	const exactSecret = "unique-exact-canary"
	value := nested{
		Name:       "safe",
		PrivateKey: "private-key-canary",
		Values: []map[string]any{{
			"Authorization": "Bearer bearer-canary-value",
			"nested":        errors.New("failed with " + exactSecret),
		}},
		Metadata: map[string]string{"access_token": "token-canary"},
	}

	var output bytes.Buffer
	logger := NewJSONLogger(&output, LogOptions{Secrets: []string{exactSecret}})
	logger.Error("request failed with "+exactSecret,
		"payload", value,
		"endpoint", mustURL(t, "https://user:password@example.test/run?token=query-canary&mode=safe"),
		"github", "ghp_abcdefghijklmnopqrstuvwxyz123456",
	)

	text := output.String()
	for _, forbidden := range []string{
		exactSecret, "private-key-canary", "bearer-canary-value", "token-canary",
		"query-canary", "password", "ghp_abcdefghijklmnopqrstuvwxyz123456",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("log contains %q: %s", forbidden, text)
		}
	}
	if strings.Count(text, redactedValue) < 6 {
		t.Fatalf("log did not contain expected redactions: %s", text)
	}
	decodeRecord(t, output.Bytes())
}

func TestRedactingHandlerCoversWithAttrsGroupsAndLogValuer(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil), "known-secret")).With(
		slog.Group("config", "client_secret", "hidden", "safe", "visible"),
	)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "message",
		slog.Any("lazy", logValue{"known-secret"}),
	)

	text := output.String()
	if strings.Contains(text, "hidden") || strings.Contains(text, "known-secret") {
		t.Fatalf("log leaked a secret: %s", text)
	}
	if !strings.Contains(text, "visible") || !strings.Contains(text, redactedValue) {
		t.Fatalf("log lost safe fields or redaction: %s", text)
	}
}

func TestJSONLoggerBoundsValuesAndHandlesCycles(t *testing.T) {
	type node struct{ Next *node }
	cycle := &node{}
	cycle.Next = cycle

	var output bytes.Buffer
	logger := NewJSONLogger(&output, LogOptions{})
	logger.Info(strings.Repeat("m", maxValueBytes+10), "long", strings.Repeat("v", maxValueBytes+10), "cycle", cycle)

	text := output.String()
	if !strings.Contains(text, "[TRUNCATED]") || !strings.Contains(text, "[CYCLE]") {
		t.Fatalf("expected bounded cycle-safe output: %s", text)
	}
}

type logValue struct{ secret string }

func (value logValue) LogValue() slog.Value { return slog.StringValue(value.secret) }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	value, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func decodeRecord(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON log: %v: %s", err, data)
	}
	return result
}
