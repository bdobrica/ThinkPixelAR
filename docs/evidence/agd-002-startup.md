# AGD-002 — agentd process lifecycle and configuration

Implemented 2026-09-21; [ADR-0023](../adr/0023-agentd-readonly-bootstrap.md).

The binary now requires fixed, read-only, bounded bootstrap metadata, rejects
unsafe configuration and conventional Kubernetes credential exposure, logs only
fixed diagnostics and responds to process termination signals. It stays explicitly
`awaiting_transport`; no harness, listener, Kubernetes client or network discovery
is started. The previous credential-free packaging baseline is now a configured
supervisor startup boundary.

Verification:

- Focused race tests cover closed JSON, malformed/oversized/deep/trailing/duplicate
  input, endpoint/identity/limit mismatch, shell/traversal rejection, writable mount
  and symlink escape, cancellation, and canary exclusion from errors/logs.
- The repository image smoke script passes against the pinned offline build
  described below, with projected-Secret-style symlinks on a read-only bind mount,
  UID/GID 65532, no network, read-only root, dropped capabilities and
  no-new-privileges. Missing bootstrap and writable bootstrap fail; accepted
  configuration stays dormant and SIGTERM exits successfully within five seconds.
- `make verify` passes. The binary dependency graph contains no Kubernetes client.

No authenticated stream, harness launch, descendant reaping, credential issuance
or live Kubernetes privilege probe is claimed. Those remain later AGD items.
The [runbook](../operations/agentd.md) records the bootstrap format and reproduction.

## Image build environment

Normal and host-network `make`/Docker builds stalled downloading module archives.
A bounded host check received only 175713 of 4709673 archive bytes in 20 seconds.
Those builds were cancelled; no dependency pins or normal Dockerfile download
behavior were weakened. `go mod verify` reported all cached modules verified.

The actual test build used the same pinned builder/runtime, source, compile flags
and final image stages. A temporary Dockerfile omitted only the network cache
warmup (`RUN go mod download`) and mounted a temporary copy of the 17 module
source directories needed by agentd, plus the graph's cached module metadata,
into `/go/pkg/mod` for compilation. `GOPROXY=off` and `--network none` made this
an offline build. The repository smoke script then exercised the resulting image.
This validates the compiled startup and runtime image; a fresh network download
through the normal multi-stage target was not completed in this environment.
No temporary Dockerfile or dependency cache is committed.
