//go:build !linux

package agentd

import (
	"context"
	"log/slog"
)

func runControlled(context.Context, *TransportBootstrap, *slog.Logger) error { return ErrProcess }
