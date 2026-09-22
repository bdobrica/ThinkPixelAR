package agentd

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrControl            = errors.New("harness control rejected")
	ErrInterruptEscalated = errors.New("harness interrupt escalated to process stop")
)

type InterruptReason uint8

const (
	InterruptCancel InterruptReason = iota + 1
	InterruptDeadline
	InterruptShutdown
)

// Handlers are trusted adapter code selected from negotiated capabilities, not
// sandbox-supplied callbacks. They must honor context, validate their registered
// payload schema, perform no deferred work after returning, and never log input.
// Success acknowledges protocol handling; it does not prove operation completion.
type SignalHandler func(context.Context, primitives.ID, []byte) error
type InterruptHandler func(context.Context, primitives.ID, InterruptReason) error
