package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRunWaitsForCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		run(ctx, slog.New(slog.NewJSONHandler(&output, nil)))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("run returned before cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run did not return after cancellation")
	}

	logs := output.String()
	for _, expected := range []string{"agent supervisor baseline started", "agent supervisor baseline stopped", "thinkpixel-agentd"} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs do not contain %q: %s", expected, logs)
		}
	}
}
