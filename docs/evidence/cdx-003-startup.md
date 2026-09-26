# CDX-003 supervised Codex startup

Date: 2026-09-26. Codex pin: **0.155.0**. Decision: [ADR-0050](../adr/0050-codex-supervised-initialization.md).

The production agentd process constructor selects the Codex driver for
`codex-app-server` and validates the exact packaged command. Successful startup
now requires the initialize/initialized exchange. The test overrides executable
and workspace locations only to exercise the same supervisor with the locally
available pinned amd64 binary, without an installed `/workspace` or image.

## Reproduce

```sh
go test -race ./internal/adapters/harness/codex ./internal/app/agentd
THINKPIXELAR_TEST_CODEX_BINARY=/absolute/path/to/pinned/codex \
  go test -race ./internal/adapters/harness/codex ./internal/app/agentd \
  -run 'Test(Initialize|Codex|Pinned)' -count=1 -v
```

The real-binary tests enforce the CDX-001 executable fingerprint. Both commands
passed. The first command needs local TCP/Unix sockets for existing agentd tests;
the restricted execution sandbox blocked them, so it was rerun with that access.
The `thinkpixel-agentd` binary also built successfully with `-trimpath -buildvcs=false`.

Verified behavior:

- Real Codex initialize/initialized through agentd process supervision, exact
  build identity, same-operation replay without a second child, and disconnect
  termination/reaping. The existing binary/schema compatibility probe also passed.
- Synthetic child handshake, restart with fresh identity/home, environment
  isolation, stderr suppression and ephemeral-home removal.
- Wrong version, child exit and stalled handshake reject startup and clean up;
  malformed, oversized, partial, duplicate-key and mismatched-ID responses fail
  closed. Blocked protocol reads/writes are cancelled without retained workers.
- Late protocol readiness cannot overwrite a stopping process observation.

## Limits

This is local startup evidence, not a model turn, full HarnessAdapter or live Kata
qualification. No provider credential was used. Thread creation/persistence is
CDX-004; turns/events/interrupt and durable state retain their existing tasks.
Protocol stdout remains private and bounded by pipe backpressure until those
operations are implemented. Rebuild the runtime image before deployment; no
new OCI digest, cluster run or platform-wide RC readiness is claimed here.
