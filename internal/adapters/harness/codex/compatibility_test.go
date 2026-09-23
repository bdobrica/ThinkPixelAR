package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This opt-in probe launches a real binary but never a thread/turn/model call.
// Only synthetic request metadata is sent; raw frames and stderr are not logged.
func TestPinnedAppServer(t *testing.T) {
	binary := os.Getenv("THINKPIXELAR_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set THINKPIXELAR_TEST_CODEX_BINARY for the pinned local protocol probe")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("this evidence pin covers Linux amd64 only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("an absolute pinned binary path is required")
	}
	f, err := os.Open(binary)
	if err != nil {
		t.Fatal("cannot read pinned executable")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	_ = f.Close()
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != LinuxAMD64SHA256 {
		t.Fatal("executable digest mismatch")
	}
	home := t.TempDir()
	// These child-only settings isolate genuine home/config paths; the calling
	// user's environment and Codex configuration are never modified or inherited.
	env := []string{"HOME=" + home, "CODEX_HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command := func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env, cmd.Dir, cmd.Stderr = env, home, io.Discard
		cmd.WaitDelay = time.Second
		return cmd
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	version, err := command(ctx, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli "+Version {
		t.Fatal("CLI version mismatch")
	}

	schemaDir := filepath.Join(home, "schema")
	if err := command(ctx, "app-server", "generate-json-schema", "--out", schemaDir).Run(); err != nil {
		t.Fatal("stable schema export failed")
	}
	// Hash deterministic sorted relative paths plus bytes; do not commit generated
	// upstream schemas or hand-edit a copied vendor protocol into this repository.
	schemaHash := sha256.New()
	methods := map[string]bool{"initialize": false, "thread/start": false, "thread/resume": false,
		"turn/start": false, "turn/interrupt": false, "turn/completed": false, "item/agentMessage/delta": false}
	count := 0
	err = filepath.WalkDir(schemaDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			return fmt.Errorf("invalid generated schema")
		}
		relative, err := filepath.Rel(schemaDir, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(schemaHash, "%s\x00", filepath.ToSlash(relative))
		_, _ = schemaHash.Write(b)
		for method := range methods {
			if strings.Contains(string(b), `"`+method+`"`) {
				methods[method] = true
			}
		}
		count++
		return nil
	})
	if err != nil || count == 0 {
		t.Fatal("schema export validation failed")
	}
	for method, found := range methods {
		if !found {
			t.Fatalf("required protocol method absent: %s", method)
		}
	}
	if hex.EncodeToString(schemaHash.Sum(nil)) != SchemaSHA256 {
		t.Fatal("stable schema fingerprint mismatch")
	}
	t.Logf("Codex %s, stable schema files=%d, sha256=%x", Version, count, schemaHash.Sum(nil))

	cmd := command(ctx, "app-server", "--listen", "stdio://", "-c", "check_for_update_on_startup=false")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal("app-server startup failed")
	}
	defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	request := func(id int, method string, params any) map[string]json.RawMessage {
		t.Helper()
		if err := json.NewEncoder(in).Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			t.Fatal("request write failed")
		}
		for n := 0; n < 32 && scanner.Scan(); n++ {
			var response map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				t.Fatal("invalid response frame")
			}
			var responseID int
			if json.Unmarshal(response["id"], &responseID) == nil && responseID == id {
				return response
			}
		}
		t.Fatal("bounded response read failed")
		return nil
	}
	if r := request(1, "thread/list", map[string]any{"limit": 1}); r["error"] == nil || r["result"] != nil {
		t.Fatal("pre-initialization request was not rejected")
	}
	initialize := map[string]any{"clientInfo": map[string]any{"name": "thinkpixelar", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": false}}
	r := request(2, "initialize", initialize)
	var result struct {
		UserAgent string `json:"userAgent"`
	}
	if r["error"] != nil || json.Unmarshal(r["result"], &result) != nil || !strings.Contains(result.UserAgent, "/"+Version) {
		t.Fatal("initialization identity mismatch")
	}
	if err := json.NewEncoder(in).Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		t.Fatal("initialized write failed")
	}
	if r := request(3, "initialize", initialize); r["error"] == nil || r["result"] != nil {
		t.Fatal("duplicate initialization was not rejected")
	}
	r = request(4, "thread/list", map[string]any{"limit": 1})
	var threads struct {
		Data []json.RawMessage `json:"data"`
	}
	if r["error"] != nil || json.Unmarshal(r["result"], &threads) != nil || threads.Data == nil || len(threads.Data) != 0 {
		t.Fatal("fresh isolated thread listing failed")
	}
	if err := in.Close(); err != nil {
		t.Fatal("stdin close failed")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal("app-server did not exit cleanly after EOF")
	}
	t.Log("stdio initialize/initialized, pre-init and duplicate rejection, empty local thread list, clean EOF: PASS")
}
