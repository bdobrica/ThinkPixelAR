package agentd

import (
	"context"
	"errors"
	"path"
	"strings"
	"unicode"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrCheckpoint = errors.New("harness checkpoint preparation failed")

// StateManifest is an untrusted candidate list, not a durable checkpoint.
// Paths are relative to /state, including the registered vendor root.
type StateManifest struct {
	VendorIdentity string
	StateFormat    string
	Paths          []string
}

// CheckpointHook is trusted adapter code selected after capability negotiation.
// It flushes/quiesces the exact process, calls ready synchronously exactly once,
// and releases quiescence before returning (including errors/cancellation).
// It must honor ctx, propagate ready's error, and retain neither callbacks nor
// inputs. No deferred work, filesystem copying or checkpoint publication occurs
// in this supervisor seam. The consumer must also honor ctx.
type CheckpointHook func(ctx context.Context, id primitives.ID, roots []string, ready func(StateManifest) error) error

type PreparedStateConsumer func(context.Context, StateManifest) error

func stateText(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\\' || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
func statePath(s string) bool { return stateText(s, 1024) && path.Clean(s) == s }
func stateRoots(roots []string) bool {
	if len(roots) == 0 || len(roots) > 32 {
		return false
	}
	for i, root := range roots {
		if !statePath(root) || !strings.HasPrefix(root, "/state/") {
			return false
		}
		for j := 0; j < i; j++ {
			if roots[j] >= root || strings.HasPrefix(root, roots[j]+"/") {
				return false
			}
		}
	}
	return true
}
func stateManifest(m StateManifest, roots []string, format string) bool {
	if !stateText(m.VendorIdentity, 256) || m.StateFormat != format || len(m.Paths) > 64 {
		return false
	}
	for i, name := range m.Paths {
		if !statePath(name) || strings.HasPrefix(name, "/") || name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return false
		}
		allowed := false
		for _, root := range roots {
			full := "/state/" + name
			if full == root || strings.HasPrefix(full, root+"/") {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
		for j := 0; j < i; j++ {
			if m.Paths[j] >= name || strings.HasPrefix(name, m.Paths[j]+"/") {
				return false
			}
		}
	}
	return true
}
