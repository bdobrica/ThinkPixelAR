//go:build !linux

package agentd

import (
	"context"
	"errors"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrProcess         = errors.New("harness process operation failed")
	ErrProcessBusy     = errors.New("harness process operation busy")
	ErrProcessStale    = errors.New("harness process identity mismatch")
	ErrProcessDeadline = errors.New("harness process deadline exceeded")
)

type Processes struct{}

func NewProcesses(Config) (*Processes, error)                   { return nil, ErrProcess }
func (*Processes) Start(context.Context) (primitives.ID, error) { return "", ErrProcess }
func (*Processes) Stop(context.Context, primitives.ID) error    { return ErrProcess }
func (*Processes) Restart(context.Context, primitives.ID) (primitives.ID, error) {
	return "", ErrProcess
}

func NewProcessesWithCapture(Config, OutputSanitizer) (*Processes, error) { return nil, ErrProcess }
func (*Processes) Output(primitives.ID) (*Capture, error)                 { return nil, ErrProcess }
