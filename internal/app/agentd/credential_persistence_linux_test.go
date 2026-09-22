package agentd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// This deliberately indiscreet fixture persists whatever environment and
// supervisor-opened credential descriptor it receives. It does not try to read
// its parent's memory or credentials by pathname: those are hostile-sandbox
// threats, not the supervisor's no-copy guarantee.
func TestCredentialPersistenceChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "credential-persistence-fixture" {
		return
	}
	root := os.Args[len(os.Args)-1]
	if os.WriteFile(filepath.Join(root, "environment"), []byte(strings.Join(os.Environ(), "\n")), 0600) != nil {
		os.Exit(81)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		os.Exit(82)
	}
	var inherited []byte
	for _, entry := range entries {
		fd := filepath.Join("/proc/self/fd", entry.Name())
		target, err := os.Readlink(fd)
		if err == nil && filepath.Base(target) == "ephemeral.credentials" {
			inherited, err = os.ReadFile(fd)
			if err != nil {
				os.Exit(83)
			}
		}
	}
	if os.WriteFile(filepath.Join(root, "descriptor"), inherited, 0600) != nil {
		os.Exit(84)
	}
	if os.WriteFile(filepath.Join(root, "prepared-ready"), nil, 0600) != nil {
		os.Exit(85)
	}
	for {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentdDoesNotPersistCredentials(t *testing.T) {
	// Values are freshly generated, not usable credentials or committed fixtures.
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("canary generation failed")
	}
	canary := hex.EncodeToString(random[:])
	t.Setenv("THINKPIXEL_EXECUTION_TOKEN", canary)
	files := transportFixture(t)
	bootstrap, err := decodeTransport(files)
	if err != nil {
		t.Fatal("bootstrap decoding failed")
	}
	defer bootstrap.Destroy()

	transient := filepath.Join(t.TempDir(), "ephemeral.credentials")
	if err := os.WriteFile(transient, []byte(canary), 0600); err != nil {
		t.Fatal("transient fixture failed")
	}
	credential, err := os.Open(transient)
	if err != nil {
		t.Fatal("transient fixture open failed")
	}
	defer credential.Close() // Keep a supervisor-owned descriptor open at launch.

	p, root := processFixture(t, "unused")
	p.config.Argv = []string{p.config.Argv[0], "-test.run=^TestCredentialPersistenceChild$", "credential-persistence-fixture", root}
	t.Setenv("HOME", root) // An inherited HOME must not enable credential persistence.
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "prepared-ready"))

	assertClean := func() {
		t.Helper()
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 3 {
			t.Fatal("unexpected durable-state write")
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(root, entry.Name()))
			if err != nil || len(data) != 0 {
				t.Fatal("environment or descriptor material persisted")
			}
		}
	}
	assertClean()

	for _, fail := range []bool{false, true} {
		c, err := NewCheckpoints(p, []string{"/state/test"}, "test/v1", func(_ context.Context, _ primitives.ID, roots []string, ready func(StateManifest) error) error {
			if len(roots) != 1 || roots[0] != "/state/test" {
				return errors.New("unexpected registration")
			}
			if fail {
				return errors.New(canary)
			}
			return ready(StateManifest{VendorIdentity: "fixture-session", StateFormat: "test/v1", Paths: []string{"test/environment"}})
		})
		if err != nil {
			t.Fatal(err)
		}
		err = c.Prepare(context.Background(), id, func(_ context.Context, m StateManifest) error {
			// Bootstrap exposes only non-secret config; checkpoint callbacks receive
			// metadata, not a bootstrap bundle, environment or process image.
			for _, value := range []any{bootstrap.Config(), m} {
				raw, e := json.Marshal(value)
				if e != nil {
					t.Error("metadata encoding failed")
					return ErrCheckpoint
				}
				for _, secret := range [][]byte{[]byte(canary), files["client.key"], files["bootstrap.proof"]} {
					escaped, _ := json.Marshal(string(secret))
					if bytes.Contains(raw, secret) || bytes.Contains(raw, escaped[1:len(escaped)-1]) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(secret))) {
						t.Error("credential entered checkpoint metadata")
						return ErrCheckpoint
					}
				}
			}
			return nil
		})
		if fail && err != ErrCheckpoint || !fail && err != nil {
			t.Fatal("unexpected preparation result")
		}
		assertClean()
	}
	// Restart must reconstruct the empty environment and descriptor set as well.
	if err := os.Remove(filepath.Join(root, "prepared-ready")); err != nil {
		t.Fatal("fixture reset failed")
	}
	next, err := p.Restart(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "prepared-ready"))
	if err := p.Stop(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	assertClean()
	raw, err := os.ReadFile(transient)
	if err != nil || string(raw) != canary {
		t.Fatal("transient source unexpectedly modified")
	}
}

func TestCheckpointRejectsTransientCredentialLocations(t *testing.T) {
	roots := []string{"/state/test"}
	for _, name := range []string{
		"/run/thinkpixel/bootstrap/client.key",
		"/run/thinkpixel/execution/token",
		"/proc/self/environ",
		"/proc/self/fd/3",
		"../../run/thinkpixel/bootstrap/bootstrap.proof",
		"test/../../run/thinkpixel/execution/token",
	} {
		if stateRoots([]string{name}) || stateManifest(StateManifest{"fixture-session", "test/v1", []string{name}}, roots, "test/v1") {
			t.Fatal("transient credential location accepted")
		}
	}
}

func TestBootstrapRejectsCredentialPersistenceConfiguration(t *testing.T) {
	for _, field := range []string{"environment", "credentials", "execution_token"} {
		var config map[string]any
		if err := json.Unmarshal(fixture(t), &config); err != nil {
			t.Fatal("fixture decoding failed")
		}
		config[field] = "synthetic-forbidden-field"
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatal("fixture encoding failed")
		}
		if _, err := DecodeConfig(raw); err != ErrConfig {
			t.Fatal("credential configuration accepted")
		}
	}
}
