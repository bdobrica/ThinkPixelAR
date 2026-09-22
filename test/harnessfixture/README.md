# Deterministic harness fixture

This test-only process and adapter provide a small structured protocol for agentd
lifecycle tests. They are excluded from production images and adapter registries.
They grant no authority and do not implement the future production HarnessAdapter
port. No model, network service, credentials or paid infrastructure is needed.

Build the standalone process with:

```sh
go build -o /tmp/thinkpixel-test-harness ./test/harnessfixture/cmd
```

Pass one argument: an unused Unix socket path in a private temporary directory.
The process creates a mode-0600 socket and writes `fixture.v1 ready` to stdout.
Read that event before connecting; PID existence alone is not protocol readiness.
Agentd tests reuse the Go test executable as the child to avoid installing a
fixture into production executable paths or weakening bootstrap validation.

`Adapter.Expect(ctx, command, reply)` sends one newline-delimited command per
connection and verifies the exact reply. Commands/replies are bounded; I/O has a
two-second ceiling narrowed by context cancellation. The process serializes
connections, with a two-second per-connection deadline and 128-byte input bound.
There is no input queue, arbitrary payload echo or background execution timer.

| Command | Allowed state | Reply / next state |
| --- | --- | --- |
| `hello fixture.v1` | any | `fixture.v1`, unchanged |
| `status` | any | current state |
| `execute` | idle | running |
| `complete`, `interrupt` | running | idle |
| `prepare` | idle | quiesced |
| `release` | quiesced | idle |
| `close` | any | closed, then clean exit |
| `crash` | any | connection closes, unsuccessful exit |

Other commands/state transitions reply `rejected`. Each successful transition
emits `fixture.v1 <state>` on stdout. The sanitizer accepts only this closed
vocabulary and readiness; unknown output fails. Commands are explicit test
scheduling points, so tests need no sleeps to simulate model progress.

The agentd integration test maps interrupt/preparation through existing hooks,
checks quiescence inside the candidate-manifest callback, then releases it. The
manifest has no files: no durable checkpoint or resume guarantee is claimed.
Restart starts idle with a new process identity. The local socket is a test
harness protocol, not the authenticated AR-to-agentd transport.

Run:

```sh
go test -race ./internal/app/agentd ./test/harnessfixture/...
```

AGD-020 must still compose the authenticated controlled-process exchange.
HNS-001–004 owns the generic port, registry, conformance and normalized events.


The separate `cmd/bootstrap` helper generates temporary, unauthoritative credentials
for the agentd image smoke test. It has no server or authorization policy and is
excluded from the production image. The smoke test mounts the generated bundle
read-only, observes the actual supervisor as PID 1 while it attempts a bounded
connection with networking disabled, runs the privilege probe, then sends SIGTERM.
Temporary keys are removed with the fixture directory; no credential is committed.
