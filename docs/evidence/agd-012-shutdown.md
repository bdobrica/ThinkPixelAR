# AGD-012 supervisor shutdown evidence

Date: 2026-09-22

## Scope

Signal-driven supervisor lifetime now invokes permanent controller shutdown.
New work is rejected, active commands are cancelled, cooperative close is bounded
and TERM/KILL cleans up the process group. The managed leader is reaped before
PID-1 orphan reaping. Repeated calls share one cleanup worker and result; callers
cannot cancel cleanup. Unresolved hooks or cleanup fail closed. Forced shutdown
never invokes checkpoint preparation.

## Verification

- `go test -race ./internal/app/agentd ./cmd/thinkpixel-agentd`: passed.
- Real-process tests verify cooperative acknowledgement followed by observed
  termination, graceful TERM exit, TERM-resistant KILL escalation, repeated
  shutdown, rejection of post-shutdown work, cancellation of pending controls,
  unresponsive shutdown hooks, caller cancellation and concurrent launch closure.
- Hook errors and panics fall back to process termination without exposing input.
- A real SIGTERM sent to an isolated supervisor helper follows NotifyContext →
  RunProcesses → Shutdown and waits for the harness to handle TERM and be reaped.
- An isolated Linux subreaper helper adopts a killed harness descendant and
  verifies Wait4 reaches ECHILD after cleanup. This requires no privileged
  container and leaves the parent test runner's child ownership unchanged.
- `make verify`: passed, including generation checks, formatting, static analysis,
  full unit/race tests, vulnerability/dependency checks, builds and API checks.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c` for
  `./internal/app/agentd`: passed (compile only).
- Staged diff whitespace and changed Markdown local-link checks: passed.

## Limits

The actual binary uses an idle controller until AGD-020 supplies authenticated
work admission. No live Kubernetes/Kata deployment was performed. The orphan
fixture uses Linux subreaper adoption rather than a PID namespace; production
calls the same orphan reaper only as PID 1. Escaped or unresolved descendants
require external Sandbox release. Local exit is not AR Execution completion or
permission to reuse credentials.

AGD-020 still validates the configured aggregate budget against the effective Pod
termination grace and sends the final authenticated observation when possible.
Current final reporting is a bounded-field local process-state log. The example
bootstrap budgets total 28 seconds; the Pod grace must leave additional margin.
See [ADR-0036](../adr/0036-agentd-bounded-supervisor-shutdown.md).
