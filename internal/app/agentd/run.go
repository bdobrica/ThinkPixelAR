package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"
)

// Run loads the protected transport bundle and requires exactly the configuration
// selected at startup. Missing credentials never leave an apparently live idle
// supervisor. No harness starts until an authenticated command is admitted.
func Run(ctx context.Context, c Config, logger *slog.Logger) error {
	if c.Validate() != nil || logger == nil {
		return ErrConfig
	}
	b, err := LoadTransport()
	if err != nil {
		return ErrConfig
	}
	defer b.Destroy()
	if !sameStartupConfig(c, b.Config()) {
		return ErrConfig
	}
	return RunWithBootstrap(ctx, b, logger)
}

func sameStartupConfig(a, b Config) bool {
	// Compare the serialized configuration contract, not protobuf's mutable
	// message caches embedded in Binding/Protocol/Limits.
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

// RunWithBootstrap owns the loaded ephemeral bundle for the supervisor lifetime.
// The trusted cutoff is a local freshness ceiling, never a grant or renewal.
func RunWithBootstrap(ctx context.Context, b *TransportBootstrap, logger *slog.Logger) error {
	if b == nil {
		return ErrConfig
	}
	defer b.Destroy()
	if logger == nil {
		return ErrConfig
	}
	c := b.Config()
	if c.Validate() != nil || c.Harness.StopGraceMS+c.Harness.KillWaitMS >= 5000 || c.ControlDeadlineUnixMS <= 0 || !time.UnixMilli(c.ControlDeadlineUnixMS).After(time.Now()) || !slices.Contains(c.RequiredCapabilities, "process-control.v1") || !slices.Contains(c.RequiredCapabilities, "rotation.v1") {
		return ErrConfig
	}
	lifetime, cancel := context.WithDeadline(ctx, time.UnixMilli(c.ControlDeadlineUnixMS))
	defer cancel()
	return runControlled(lifetime, b, logger)
}

// RunProcesses binds an admitted controller to the supervisor lifetime. It never
// launches work. It is also used by lifecycle tests without transport.
func RunProcesses(ctx context.Context, p *Processes, hook InterruptHandler, logger *slog.Logger) error {
	if p == nil || logger == nil {
		return ErrConfig
	}
	<-ctx.Done()
	if err := p.Shutdown(context.Background(), hook); err != nil {
		logger.Error("agent supervisor cleanup failed", "component", "thinkpixel-agentd", "process_state", p.Status().State.String())
		return err
	}
	logger.Info("agent supervisor stopped", "component", "thinkpixel-agentd", "process_state", p.Status().State.String())
	return nil
}
