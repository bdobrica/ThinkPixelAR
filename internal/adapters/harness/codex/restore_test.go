package codex

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestRestoreState(t *testing.T) {
	raw := []byte(`{"type":"session_meta","payload":{"id":"` + testThreadID + `","cwd":"/workspace","cli_version":"` + Version + `","timestamp":"2026-09-27T10:11:12Z"}}` + "\n" + `{"type":"response_item","payload":{}}` + "\n")
	state := RestoreState{ThreadID: testThreadID, Protocol: Version, Rollout: raw, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	name, err := state.RestorePath("/workspace")
	if err != nil || name != "sessions/2026/09/27/rollout-2026-09-27T10-11-12-"+testThreadID+".jsonl" {
		t.Fatal(name, err)
	}
	for _, mode := range []string{"digest", "thread", "version", "cwd", "truncated", "duplicate-meta", "oversize", "invalid-json", "path"} {
		t.Run(mode, func(t *testing.T) {
			s := state
			switch mode {
			case "digest":
				s.SHA256 = strings.Repeat("0", 64)
			case "thread":
				s.ThreadID = "01950000-0000-7000-8000-000000000098"
			case "version":
				s.Protocol = "0.155.1"
			case "cwd":
				s.Rollout = []byte(strings.Replace(string(raw), "/workspace", "/elsewhere", 1))
			case "truncated":
				s.Rollout = raw[:len(raw)-1]
			case "duplicate-meta":
				s.Rollout = append(append([]byte{}, raw...), raw...)
			case "oversize":
				s.Rollout = []byte(strings.Repeat("x", MaxRestoreBytes+1))
			case "invalid-json":
				s.Rollout = []byte("{broken}\n")
			case "path":
				s.ThreadID = "../../auth.json"
			}
			if mode != "digest" {
				s.SHA256 = fmt.Sprintf("%x", sha256.Sum256(s.Rollout))
			}
			if _, err := s.RestorePath("/workspace"); err == nil {
				t.Fatal("invalid restore accepted")
			}
		})
	}
}
