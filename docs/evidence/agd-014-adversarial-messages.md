# AGD-014 adversarial message evidence

Date: 2026-09-22

## Scope

Adds tests for the existing authenticated transport and codec guards, without
changing production behavior, wire contracts or architecture. Decisions remain
in ADRs [0024](../adr/0024-agentd-authenticated-grpc-channel.md),
[0027](../adr/0027-agentd-admission-composition.md) and
[0037](../adr/0037-agentd-rotation-reconnect-recovery.md).

Real loopback TCP/mTLS tests use a private test client codec to bypass outgoing
validation and inject raw application frames after a valid Hello/Welcome:

- truncated protobuf, invalid wire type and unknown envelope/nested fields;
- missing body, unknown enum and wrong message direction;
- old/future connection epochs and zero sequence;
- unnegotiated rotation;
- negotiated event/diagnostic size violations and the 1 MiB wire ceiling;
- a positive control exactly at the negotiated event-payload limit.

Invalid frames close the stream within the test deadline and never reach the
application frame policy. The positive control reaches policy and is acknowledged.

Replay tests use an explicit test policy: reused sequence, gaps and conflicting
message identity are denied before delivery; an identical replay approved by the
policy is delivered for acknowledgement. This tests enforcement of the policy
boundary, not durable mutation deduplication or exactly-once effects. Concrete
production frame policies, replay reconciliation and binary dispatch remain
AGD-020. Existing AGD-005/013 tests cover one-time bootstrap replay, persisted
epoch fencing and credential renewal replay separately.

A bounded codec fuzz target accepts arbitrary bytes and checks that successfully
decoded messages obey size/known-field bounds and preserve meaning on a protobuf
round trip. Existing codec tests retain recursion-depth/oversized-input coverage.
No new duplicate-field rule is imposed on standard protobuf decoding.

## Verification

- `go test -race ./internal/adapters/sandboxtransport/grpc`: passed.
- Five-second decoder fuzz run with two workers: passed, 34,857 executions.
- `make verify`: passed.

## Limits

No live Kubernetes, vendor harness or PostgreSQL deployment was needed for these
message tests. The replay policy fixture is not a production idempotency store.
No durable operation replay guarantee is inferred from transport acceptance.
