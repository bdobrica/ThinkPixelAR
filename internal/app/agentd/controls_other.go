//go:build !linux

package agentd

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type Controls struct{}

func NewControls(*Processes, uint32, map[string]SignalHandler, InterruptHandler) (*Controls, error) {
	return nil, ErrControl
}
func (*Controls) Signal(context.Context, primitives.ID, string, []byte) error     { return ErrControl }
func (*Controls) Interrupt(context.Context, primitives.ID, InterruptReason) error { return ErrControl }
