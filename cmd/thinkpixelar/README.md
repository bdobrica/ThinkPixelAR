# thinkpixelar

This command runs the AR control-plane HTTP server. Configuration is loaded as
documented in [`docs/configuration.md`](../../docs/configuration.md). The current
baseline exposes `/livez`, `/readyz`, and `/metrics`; application routes are
added by later implementation phases.
