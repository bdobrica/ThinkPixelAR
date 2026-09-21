package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestStartupRejectsArgumentsAndCredentialEnvironmentWithoutEcho(t *testing.T) {
	for _, credential := range []bool{false, true} {
		var output bytes.Buffer
		args := []string{"CANARY_UNTRUSTED_ARGUMENT"}
		if credential {
			t.Setenv("KUBECONFIG", "CANARY_KUBE_PATH")
			args = nil
		}
		if code := entry(context.Background(), args, slog.New(slog.NewJSONHandler(&output, nil))); code != 1 {
			t.Fatal("startup accepted")
		}
		if strings.Contains(output.String(), "CANARY") {
			t.Fatal("startup leaked input")
		}
	}
}
