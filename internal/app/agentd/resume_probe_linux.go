package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"golang.org/x/sys/unix"
)

// ResumeProbe is delivered through the operator's Kubernetes exec channel into
// isolated, quiescent candidate compute. It is not a transport command and does
// not authorize an Execution. Only the three fixed export paths are supported.
type ResumeProbe struct {
	Config                       Config
	Workspace, Rollout, Metadata []byte
}

type ResumeProof struct {
	Thread, WorkspaceSHA256, RolloutSHA256 string
}

func probeHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ProbeResume restores the exact thread, then reaps the process and removes its
// fresh home. No turn capability, input, listener, credential or model route is
// configured. Its observations still require independent controller verification.
func ProbeResume(ctx context.Context, in ResumeProbe) (ResumeProof, error) {
	var proof ResumeProof
	if len(in.Workspace) == 0 || len(in.Workspace) > 1<<20 || len(in.Rollout) > 1<<20 || len(in.Metadata) > 4096 || in.Config.Validate() != nil || slices.Contains(in.Config.Capabilities, control.TurnCapability) {
		return proof, ErrCheckpoint
	}
	var selected codex.RestoreState
	d := json.NewDecoder(bytes.NewReader(in.Metadata))
	d.DisallowUnknownFields()
	if d.Decode(&selected) != nil || d.Decode(new(any)) != io.EOF || len(selected.Rollout) != 0 {
		return proof, ErrCheckpoint
	}
	selected.Rollout = in.Rollout
	if _, err := selected.RestorePath("/workspace"); err != nil {
		return proof, ErrCheckpoint
	}
	binary, err := os.ReadFile(codex.Command()[0])
	if err != nil || probeHash(binary) != codex.LinuxARM64SHA256 {
		return proof, ErrCheckpoint
	}
	// A crashed caller cannot leave a userspace lock held. This also excludes two
	// overlapping exec streams after a lost controller response.
	lock, err := os.OpenFile("/tmp/thinkpixel-resume.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return proof, ErrCheckpoint
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return proof, ErrProcessBusy
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	for path, raw := range map[string][]byte{"/workspace/context.txt": in.Workspace, "/state/rollout.jsonl": in.Rollout, "/state/restore.json": in.Metadata} {
		if err = restoreProbeFile(path, raw); err != nil {
			return proof, err
		}
	}
	p, err := NewProcessesWithCodexRestore(in.Config, selected)
	if err != nil {
		return proof, ErrCheckpoint
	}
	p.resumeProbe = true
	defer p.Shutdown(context.Background(), nil)
	id, err := p.Start(ctx)
	if err != nil {
		return proof, ErrCheckpoint
	}
	if p.current.threadID != selected.ThreadID || p.executeTurn || p.Stop(ctx, id) != nil {
		return proof, ErrCheckpoint
	}
	return ResumeProof{selected.ThreadID, probeHash(in.Workspace), probeHash(in.Rollout)}, nil
}

// Atomic, no-clobber writes make retries after a partial upload safe. Existing
// data must match exactly; the probe never rewinds a workspace after user work.
func restoreProbeFile(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err == nil {
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != int64(len(raw)) {
			return ErrCheckpoint
		}
		got, e := io.ReadAll(io.LimitReader(f, int64(len(raw))+1))
		if e != nil || !bytes.Equal(got, raw) {
			return ErrCheckpoint
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return ErrCheckpoint
	}
	f, err = os.CreateTemp(filepath.Dir(path), ".resume-")
	if err != nil {
		return ErrCheckpoint
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return ErrCheckpoint
	}
	if f.Sync() != nil || os.Link(f.Name(), path) != nil {
		return ErrCheckpoint
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrCheckpoint
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return ErrCheckpoint
	}
	return nil
}
