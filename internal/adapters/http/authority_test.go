package http

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
)

type fixedIdentity authority.Identity

func (i fixedIdentity) Identity() authority.Identity { return authority.Identity(i) }

func TestAuthorityDiagnosticsFailClosed(t *testing.T) {
	for _, id := range []authority.Identity{
		{}, {Mode: "local", Issuer: "thinkpixelag"},
		{Mode: "thinkpixelag", Issuer: authority.LocalIssuer},
		{Mode: "thinkpixelag", Issuer: "https://issuer?token=secret"},
		{Mode: "unknown", Issuer: "unknown"},
	} {
		if _, err := NewServer(Options{Clock: clock.UTC{}, Authority: fixedIdentity(id)}); err == nil {
			t.Fatalf("accepted %v", id)
		}
	}
	server := testServer(t, func(context.Context) error { return nil }, nil, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/authorityz", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"authority_mode":"unconfigured"`) || !strings.Contains(response.Body.String(), `"authority_issuer":""`) {
		t.Fatalf("missing absent-adapter diagnostic: %s", response.Body)
	}
}

func TestAGDiagnosticsDoNotClaimAvailability(t *testing.T) {
	server, err := NewServer(Options{Config: config.Default().HTTP, Clock: clock.Fixed{Time: time.Now()}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authority: fixedIdentity{Mode: "thinkpixelag", Issuer: "thinkpixelag/test"}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/authorityz", nil))
	if !strings.Contains(response.Body.String(), `"authority_mode":"thinkpixelag"`) || !strings.Contains(response.Body.String(), "not an availability or admission check") {
		t.Fatal(response.Body)
	}
}
