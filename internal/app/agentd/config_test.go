package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../deploy/agentd/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestConfigRejectsInvalidInput(t *testing.T) {
	raw := fixture(t)
	if _, err := DecodeConfig(raw); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(map[string]any){
		"unknown":        func(c map[string]any) { c["kubeconfig"] = "CANARY" },
		"version":        func(c map[string]any) { c["version"] = 2 },
		"http":           func(c map[string]any) { c["endpoint"] = "http://ar-transport.example.invalid:8443" },
		"host":           func(c map[string]any) { c["server_name"] = "another.invalid" },
		"ip":             func(c map[string]any) { c["endpoint"] = "https://127.0.0.1"; c["server_name"] = "127.0.0.1" },
		"empty port":     func(c map[string]any) { c["endpoint"] = "https://ar-transport.example.invalid:" },
		"empty query":    func(c map[string]any) { c["endpoint"] = "https://ar-transport.example.invalid?" },
		"empty fragment": func(c map[string]any) { c["endpoint"] = "https://ar-transport.example.invalid#" },
		"userinfo":       func(c map[string]any) { c["endpoint"] = "https://CANARY@ar-transport.example.invalid:8443" },
		"path":           func(c map[string]any) { c["endpoint"] = "https://ar-transport.example.invalid:8443/extra" },
		"port":           func(c map[string]any) { c["endpoint"] = "https://ar-transport.example.invalid:99999" },
		"shell": func(c map[string]any) {
			c["harness"].(map[string]any)["argv"] = []string{"/usr/bin/sh", "-c", "echo CANARY"}
		},
		"relative": func(c map[string]any) { c["harness"].(map[string]any)["argv"] = []string{"harness"} },
		"traversal": func(c map[string]any) {
			c["harness"].(map[string]any)["argv"] = []string{"/usr/bin/../../workspace/tool"}
		},
		"workspace exe": func(c map[string]any) { c["harness"].(map[string]any)["argv"] = []string{"/workspace/tool"} },
		"cwd":           func(c map[string]any) { c["harness"].(map[string]any)["working_directory"] = "/workspace/../state" },
		"timeout":       func(c map[string]any) { c["harness"].(map[string]any)["kill_wait_ms"] = 0 },
		"limit":         func(c map[string]any) { c["limits"].(map[string]any)["buffered_bytes"] = 16777217 },
		"binding":       func(c map[string]any) { c["binding"].(map[string]any)["attempt_id"] = "CANARY" },
	} {
		t.Run(name, func(t *testing.T) {
			var c map[string]any
			if json.Unmarshal(raw, &c) != nil {
				t.Fatal("fixture")
			}
			edit(c)
			bad, _ := json.Marshal(c)
			if _, err := DecodeConfig(bad); err != ErrConfig {
				t.Fatal("invalid configuration accepted", err)
			}
		})
	}
	for _, bad := range [][]byte{nil, bytes.Repeat([]byte{' '}, MaxConfigBytes+1), append(bytes.Clone(raw), raw...), []byte(`{"version":1,"version":1}`), []byte(`{"Version":1}`), []byte(`{"version":null}`), []byte(strings.Repeat("[", 20) + strings.Repeat("]", 20))} {
		if _, err := DecodeConfig(bad); err != ErrConfig {
			t.Fatal("malformed configuration accepted", err)
		}
	}
}
func TestRunWaitsForCancellationWithoutLaunchingOrLoggingConfig(t *testing.T) {
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	c.Harness.Argv = append(c.Harness.Argv, "CANARY_ARGUMENT")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, slog.New(slog.NewJSONHandler(&output, nil))) }()
	select {
	case <-done:
		t.Fatal("early exit")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	if strings.Contains(output.String(), "CANARY") || strings.Contains(output.String(), c.Endpoint) || !strings.Contains(output.String(), "awaiting_transport") {
		t.Fatal("unsafe startup telemetry")
	}
}
