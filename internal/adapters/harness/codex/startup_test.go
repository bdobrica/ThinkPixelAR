package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

func TestInitialize(t *testing.T) {
	good := `{"id":1,"result":{"userAgent":"thinkpixelar/0.155.0 (Linux) test","platformFamily":"unix","platformOs":"linux","codexHome":"private-canary"}}` + "\n"
	for name, tc := range map[string]struct {
		response string
		want     error
	}{
		"ready":              {good, nil},
		"version":            {strings.Replace(good, "0.155.0", "0.155.01", 1), harness.ErrIncompatible},
		"prerelease":         {strings.Replace(good, "0.155.0", "0.155.0-beta", 1), harness.ErrIncompatible},
		"wrong client":       {strings.Replace(good, "thinkpixelar/", "other/", 1), harness.ErrIncompatible},
		"wrong id":           {strings.Replace(good, `"id":1`, `"id":2`, 1), harness.ErrProtocol},
		"duplicate id":       {strings.Replace(good, `"id":1`, `"id":2,"id":1`, 1), harness.ErrProtocol},
		"duplicate identity": {`{"id":1,"result":{"userAgent":"bad","userAgent":"thinkpixelar/0.155.0"}}` + "\n", harness.ErrProtocol},
		"error":              {`{"id":1,"error":{"message":"secret-canary"}}` + "\n", harness.ErrProtocol},
		"both":               {strings.Replace(good, `"id":1`, `"id":1,"error":{}`, 1), harness.ErrProtocol},
		"missing":            {"{\"id\":1,\"result\":{}}\n", harness.ErrProtocol},
		"partial":            {strings.TrimSuffix(good, "\n"), harness.ErrProtocol},
		"oversized":          {strings.Repeat("x", maxHandshakeBytes) + "\n", harness.ErrProtocol},
		"notification":       {"{\"method\":\"unknown\"}\n", harness.ErrProtocol},
	} {
		t.Run(name, func(t *testing.T) {
			input, in := io.Pipe()
			out, output := io.Pipe()
			c := NewClient(in, out)
			defer c.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer input.Close()
				defer output.Close()
				s := bufio.NewScanner(input)
				if !s.Scan() {
					t.Error("missing initialize")
					return
				}
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Capabilities struct {
							Experimental bool `json:"experimentalApi"`
						} `json:"capabilities"`
					} `json:"params"`
				}
				if json.Unmarshal(s.Bytes(), &request) != nil || request.ID != 1 || request.Method != "initialize" || request.Params.Capabilities.Experimental {
					t.Error("invalid initialization request")
					return
				}
				_, _ = io.WriteString(output, tc.response)
				if name == "partial" {
					return
				}
				if s.Scan() {
					if tc.want != nil || string(s.Bytes()) != `{"method":"initialized","params":{}}` {
						t.Error("unexpected acknowledgement")
					}
				} else if tc.want == nil {
					t.Error("missing initialized")
				}
			}()
			err := c.Initialize(t.Context())
			if !errors.Is(err, tc.want) {
				t.Fatalf("unexpected closed error: %v", err)
			}
			if err := c.Initialize(t.Context()); err != harness.ErrConflict {
				t.Fatal("initialization repeated")
			}
			c.Close()
			<-done
		})
	}
}

func TestInitializeCancellation(t *testing.T) {
	for _, blocked := range []string{"write", "read"} {
		t.Run(blocked, func(t *testing.T) {
			input, in := io.Pipe()
			out, output := io.Pipe()
			defer input.Close()
			defer output.Close()
			c := NewClient(in, out)
			defer c.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Initialize(ctx) }()
			if blocked == "read" {
				if !bufio.NewScanner(input).Scan() {
					t.Fatal("missing request")
				}
			}
			cancel()
			if err := <-done; err != harness.ErrOutcomeUnknown {
				t.Fatal("cancellation not bounded", err)
			}
		})
	}
}
