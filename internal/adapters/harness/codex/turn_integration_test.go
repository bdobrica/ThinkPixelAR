package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A real pinned App Server accepts the turn; a loopback-only model fixture
// prevents provider calls and uses no API keys or inherited operator state.
func TestPinnedTurnStart(t *testing.T) {
	binary := os.Getenv("THINKPIXELAR_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set pinned Codex binary")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute binary required")
	}
	f, e := os.Open(binary)
	if e != nil {
		t.Fatal("binary unavailable")
	}
	hash := sha256.New()
	_, e = io.Copy(hash, f)
	_ = f.Close()
	if e != nil || hex.EncodeToString(hash.Sum(nil)) != LinuxAMD64SHA256 {
		t.Fatal("binary pin mismatch")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	args := append(Command()[1:], "-c", `model="fixture"`, "-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="fixture"`, "-c", "model_providers.fixture.base_url="+strconv.Quote(server.URL+"/v1"), "-c", `model_providers.fixture.wire_api="responses"`, "-c", `model_providers.fixture.requires_openai_auth=false`)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.Dir = home
	cmd.Stderr = io.Discard
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if cmd.Start() != nil {
		t.Fatal("start failed")
	}
	c := NewClient(in, out)
	defer func() { c.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if e := c.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := c.StartThread(ctx, home); e != nil {
		t.Fatal(e)
	}
	id, e := c.StartTurn(ctx, turnOperation, turnInput, "Return one word; do not run tools.")
	if e != nil || !ValidThreadID(id) {
		t.Fatal("real turn acceptance", e)
	}
	if again, e := c.StartTurn(ctx, turnOperation, turnInput, "Return one word; do not run tools."); e != nil || again != id {
		t.Fatal("real turn replay", e)
	}
	t.Log("real pinned turn accepted; no provider/model completion claimed")
}
