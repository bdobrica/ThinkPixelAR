# ADR-0023: Agentd read-only bootstrap and process startup

Status: Accepted 2026-09-21

## Context

The agentd contract requires closed, read-only bootstrap configuration and no
Kubernetes credentials inside the supervisor. Generic control-plane environment
configuration must not become a second sandbox identity or authority source.
AGD-002 precedes transport and harness command implementation.

## Decision

Agentd reads only `/run/thinkpixel/bootstrap/config.json`. It accepts no command
line arguments or environment configuration overrides. The file is a bounded
64 KiB regular file on a read-only filesystem, with no write permission bits.
Linux descriptor-based filesystem checks avoid a pathname stat/read race;
Go OpenRoot permits projected-Secret links within the bootstrap root while
rejecting escapes. Nonblocking open prevents a substituted FIFO blocking startup.
Non-Linux startup fails closed.

JSON is closed and rejects duplicate keys, non-lowercase field names, nulls,
trailing documents, excessive nesting and unknown fields. Configuration carries
non-secret protocol/binding/build/adapter metadata, finite limits, a TLS DNS
endpoint and direct harness argv. Initial immutable image layout places harness
executables below `/usr/bin` or `/usr/local/bin`; shell/env entrypoints and
noncanonical paths are rejected. Working directory is `/workspace`. Launch and
termination budgets are positive and capped. Credential values and execution
environment maps are absent from this format.

Startup rejects a KUBECONFIG environment variable or conventional mounted/local
Kubernetes credential paths. This is a guard, not proof that hostile software
cannot conceal credentials; effective Pod security remains external under
ADR-0013. The binary imports no Kubernetes client and performs no cluster or
metadata discovery. Configuration is never logged; failures use fixed messages.

The configured process responds to SIGINT/SIGTERM and remains explicitly
`awaiting_transport`, with no listener or child, until AGD-003 supplies the
transport. This state is not Ready or authority to launch. Harness termination
and child reaping are implemented by AGD-006/009/012, not claimed here.

## Consequences

Missing bootstrap is a startup error rather than a silently usable baseline.
Operators project bootstrap read-only (the existing sandbox template uses 0440
with its sandbox group; the non-secret smoke fixture uses 0444). The example contains only
synthetic metadata, no valid identity or credential. The image smoke now checks
actual read-only mount acceptance, unsafe startup rejection and SIGTERM exit.

See [configuration/runbook](../operations/agentd.md) and
[AGD-002 evidence](../evidence/agd-002-startup.md).
