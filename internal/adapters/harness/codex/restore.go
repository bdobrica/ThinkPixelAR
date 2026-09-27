package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

const MaxRestoreBytes = 16 << 20

// RestoreState is a single pinned Codex rollout selected by trusted checkpoint
// composition. It is confidential vendor content, never bootstrap configuration
// or authority. The caller must validate the checkpoint, runtime pins, tenant,
// current fences and credential exclusions before supplying these bytes.
// No vendor-selected paths, home/config/auth files or databases are imported.
type RestoreState struct {
	ThreadID, Protocol, SHA256 string
	Rollout                    []byte
}

func (RestoreState) String() string     { return "[restricted Codex restore state]" }
func (s RestoreState) GoString() string { return s.String() }

// RestorePath checks the bounded object and its session metadata, then derives
// the only relative destination permitted inside a fresh CODEX_HOME. The digest
// must come from validated checkpoint metadata, not from the untrusted payload.
func (s RestoreState) RestorePath(cwd string) (string, error) {
	if !ValidThreadID(s.ThreadID) || s.Protocol != Version || len(s.Rollout) == 0 || len(s.Rollout) > MaxRestoreBytes || s.Rollout[len(s.Rollout)-1] != '\n' || !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return "", harness.ErrCheckpoint
	}
	digest := sha256.Sum256(s.Rollout)
	if s.SHA256 != hex.EncodeToString(digest[:]) {
		return "", harness.ErrCheckpoint
	}
	var stamp time.Time
	first := true
	for line := range bytes.SplitSeq(bytes.TrimSuffix(s.Rollout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) > 1<<20 {
			return "", harness.ErrCheckpoint
		}
		row, err := object(line)
		if err != nil {
			return "", harness.ErrCheckpoint
		}
		var kind string
		if json.Unmarshal(row["type"], &kind) != nil || kind == "" || row["payload"] == nil {
			return "", harness.ErrCheckpoint
		}
		if !first {
			if kind == "session_meta" {
				return "", harness.ErrCheckpoint
			}
			continue
		}
		first = false
		meta, err := object(row["payload"])
		var id, directory, version, timestamp string
		if kind != "session_meta" || err != nil || json.Unmarshal(meta["id"], &id) != nil || id != s.ThreadID || json.Unmarshal(meta["cwd"], &directory) != nil || directory != cwd || json.Unmarshal(meta["cli_version"], &version) != nil || version != Version || json.Unmarshal(meta["timestamp"], &timestamp) != nil {
			return "", harness.ErrCheckpoint
		}
		stamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || stamp.Year() < 1970 || stamp.Year() > 9999 {
			return "", harness.ErrCheckpoint
		}
	}
	stamp = stamp.UTC()
	return "sessions/" + stamp.Format("2006/01/02") + "/rollout-" + stamp.Format("2006-01-02T15-04-05") + "-" + s.ThreadID + ".jsonl", nil
}
