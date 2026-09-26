package agentd

import (
	"os"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
)

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
