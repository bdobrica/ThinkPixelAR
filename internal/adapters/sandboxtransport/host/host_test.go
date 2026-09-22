package agentdhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	networking "k8s.io/api/networking/v1"
)

func TestLoadRequiresExplicitLocalTenantBinding(t *testing.T) {
	tenant, _ := primitives.NewID(time.Now())
	binding, _ := primitives.NewID(time.Now())
	c := Config{Version: 1, Mode: "local", ListenAddress: "127.0.0.1:8443", Tenants: []primitives.ID{tenant}, Bindings: []Binding{{TenantID: tenant, SandboxID: binding, NetworkPolicy: &networking.NetworkPolicy{}}}}
	path := filepath.Join(t.TempDir(), "listener.json")
	write := func(c Config) {
		raw, _ := json.Marshal(c)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(c)
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "thinkpixelag"} {
		bad := c
		bad.Mode = mode
		write(bad)
		if _, err := Load(path); err != ErrHost {
			t.Fatal("unsupported authority mode", err)
		}
	}
	bad := c
	bad.Tenants = nil
	write(bad)
	if _, err := Load(path); err != ErrHost {
		t.Fatal("implicit tenant enumeration", err)
	}
	bad = c
	bad.Bindings = append(append([]Binding(nil), c.Bindings...), c.Bindings[0])
	write(bad)
	if _, err := Load(path); err != ErrHost {
		t.Fatal("ambiguous binding plan", err)
	}
	write(c)
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != ErrHost {
		t.Fatal("writable config accepted", err)
	}
}

func TestPrivateKeysAndBoundedReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("CANARY_NOT_A_KEY"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := read(path, 32768, true); err != ErrHost {
		t.Fatal("public key file accepted", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(path, 2, true); err != ErrHost {
		t.Fatal("oversized input accepted", err)
	}
}
