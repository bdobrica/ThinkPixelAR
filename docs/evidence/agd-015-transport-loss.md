# AGD-015 transport-loss evidence

Date: 2026-09-22

## Scope

Regression coverage for the existing liveness and cancellation decisions in
[ADR-0024](../adr/0024-agentd-authenticated-grpc-channel.md) and
[ADR-0037](../adr/0037-agentd-rotation-reconnect-recovery.md). No production
behavior, contracts, dependencies or architectural decisions change.

Real loopback TCP/mTLS streams first exchange heartbeats and acknowledgements
for longer than the negotiated 500 ms liveness window. This positive control
checks that incoming application traffic renews both watchdogs. Each case then
injects one fault:

- abrupt underlying client socket closure;
- AR transport server shutdown;
- silent client-to-server traffic loss;
- silent server-to-client traffic loss;
- silent traffic loss in both directions.

A private test dialer discards encrypted bytes after the valid handshake for the
silent-loss cases. TCP stays open and local writes can report success. The test
continues sending while waiting for a receive, checks closure within a two-second
scheduling allowance (before the five-second caller deadline), cancellation of
the client Session, rejection of subsequent sends, server handler exit and exactly
one lease release. Ephemeral certificate material is generated in memory.

## Verification

- `go test -race ./internal/adapters/sandboxtransport/grpc -run TestTransportLoss -count=3`: passed (all five cases, three repetitions).
- `go test -race ./internal/adapters/sandboxtransport/grpc`: passed.
- `make verify`: passed.

## Limits

These tests model half-open/silent-loss behavior at the encrypted stream boundary;
they do not qualify a real firewall, load balancer, Kubernetes network partition
or reconnect storm. Existing AGD-013 tests cover reconnect/epoch replacement.
AGD-020 still owns binary composition of disconnect hooks with local harness
stop/fencing and durable ambiguous-operation reconciliation. Stream closure is
not proof that a harness stopped or that an external operation had no effect.
