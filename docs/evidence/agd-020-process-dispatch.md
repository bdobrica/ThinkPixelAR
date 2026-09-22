# AGD-020 — authenticated process dispatcher evidence

Date: 2026-09-22. Component integration; **AGD-020 remains open**.

## Implemented exchange

`TestAuthenticatedProcessControlExchange` runs real loopback mTLS using separate
server/client CA roles, the generated gRPC transport, the new process dispatcher
and the AGD-017 structured child process. AR-side test code sends START, an
identical operation retry with fresh transport metadata, STATUS and INTERRUPT.
It checks correlated acknowledgements and receives the allowlisted `fixture.v1
ready` output on the authenticated stream, then verifies the native fixture
handshake. Interrupt stops the child. The ledger records three operations rather
than executing the duplicate START.

`TestProcessControlReplayAndFencing` checks conflicting operation IDs, repeated
START with a new operation, stale handle rejection, disconnect cleanup and retry
without relaunch. Protocol tests reject payload/argv injection, artifacts, unknown
schemas/commands, configuration/digest/identity mismatches, expired commands,
conflicting frame replay, gaps and regressions. Race tests cover the dispatcher
alongside the existing process/capture/shutdown suite.

The fixture authorizer is test-only. It does not supply production authority,
PostgreSQL command persistence, effective Kubernetes proof, bootstrap publication,
cleanup scheduling or recovery reconciliation. No live cluster, image qualification,
binary-to-binary exchange or production-ready lifecycle reducer is claimed.

## Validation

Passed:

```sh
go test -race ./internal/app/agentd ./internal/adapters/sandboxtransport/control -run 'TestAuthenticatedProcessControlExchange|TestProcessControl|TestClosedCommands|TestSequenceReplay' -count=3
go test -race ./internal/app/agentd ./internal/adapters/sandboxtransport/control
make verify
```

The aggregate gate passed static analysis, unit/race tests, vulnerability/license
checks, builds and generated-artifact checks. Local Markdown links, staged
whitespace and repository hygiene also passed. Database and live-provider tests
remain opt-in and were not exercised by this gate.

## Remaining closure work

Wire concrete AR authority/materialization and durable frame/operation policies,
the transport listener and agentd entrypoint, bootstrap delivery and tenant cleanup,
rotation/reconnect and replacement recovery. Verify final shutdown reporting and
effective Pod budget. Repeat the controlled-process acceptance with those actual
policies and persistence before closing AGD-020 or AGD-019. See the
[Phase 4 acceptance checklist](../phase-4-evidence.md).
