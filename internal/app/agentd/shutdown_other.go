//go:build !linux

package agentd

import "context"

func (*Processes) Shutdown(context.Context, InterruptHandler) error { return ErrProcess }
