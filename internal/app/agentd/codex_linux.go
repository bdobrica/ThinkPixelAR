package agentd

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
)

// NewProcessesWithCodexRestore configures a fresh supervisor from trusted,
// validated checkpoint content. Start still requires authenticated admission;
// restoration grants no permission to execute a turn. It uses suppressed capture
// and never inherits the previous process's environment or filesystem home.
// This is not a wire configuration field or an arbitrary filesystem import API.
func NewProcessesWithCodexRestore(c Config, state codex.RestoreState) (*Processes, error) {
	p, err := NewProcessesWithCapture(c, nil)
	if err != nil {
		return nil, err
	}
	if !p.codex || !p.createThread {
		return nil, ErrConfig
	}
	if _, err := state.RestorePath(p.config.WorkingDirectory); err != nil {
		return nil, err
	}
	state.Rollout = bytes.Clone(state.Rollout)
	p.restore = &state
	return p, nil
}

// prepareCodex runs only after bootstrap selection and command admission. It
// connects private protocol pipes, not diagnostic capture, to the pinned child.
// Child environment values are constructed here; nothing is inherited from agentd.
func (c *child) prepareCodex() (func(), error) {
	home, err := os.MkdirTemp("/tmp", "thinkpixel-codex-")
	if err != nil {
		return nil, ErrProcess
	}
	c.codexHome = home
	if os.Mkdir(home+"/codex", 0700) != nil {
		_ = c.closeCodex()
		return nil, ErrProcess
	}
	if state := c.owner.restore; state != nil {
		name, err := state.RestorePath(c.owner.config.WorkingDirectory)
		if err == nil {
			name = filepath.Join(home, "codex", name)
			err = os.MkdirAll(filepath.Dir(name), 0700)
			if err == nil {
				err = os.WriteFile(name, state.Rollout, 0600)
			}
		}
		if err != nil {
			_ = c.closeCodex()
			return nil, ErrProcess
		}
	}
	in, input, err := os.Pipe()
	if err != nil {
		_ = c.closeCodex()
		return nil, ErrProcess
	}
	output, out, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		_ = input.Close()
		_ = c.closeCodex()
		return nil, ErrProcess
	}
	c.codex = codex.NewClient(input, output)
	c.cmd.Stdin, c.cmd.Stdout = in, out
	c.cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + home + "/codex", "PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	return func() { _ = in.Close(); _ = out.Close() }, nil
}

func (c *child) closeCodex() error {
	if c.codex != nil {
		c.codex.Close()
	}
	if c.codexHome != "" {
		return os.RemoveAll(c.codexHome)
	}
	return nil
}
