# AGD-017 deterministic harness fixture evidence

Date: 2026-09-22

Adds a test-only process and protocol driver described in the
[fixture runbook](../../test/harnessfixture/README.md). No production behavior,
wire schema, dependency or architecture changes. Existing decisions remain in
ADRs [0030](../adr/0030-agentd-bounded-process-control.md),
[0031](../adr/0031-agentd-bounded-output-capture.md),
[0033](../adr/0033-agentd-signal-interrupt-control.md) and
[0034](../adr/0034-agentd-checkpoint-preparation.md).

Real child-process tests cover version mismatch, structured readiness and capture,
explicit execution/completion, illegal-state rejection, cooperative interrupt,
checkpoint quiescence/release through the existing hook, clean close, fresh
restart identity, stale-ID rejection and nonzero-exit observation. The fixture
provides explicit scheduling points rather than simulated work delays. Adapter
tests check cancellation of a pending response and connection cleanup, command
bounds and rejection of unclassified output.

## Verification

- Focused lifecycle/adapter race tests, three repetitions: passed.
- `go test -race ./internal/app/agentd ./test/harnessfixture/...`: passed.
- `make verify`: passed.

## Limits

The fixture is not registered as a production HarnessAdapter; that port remains
HNS-001. No durable state, checkpoint publication, vendor resume, external effect
or execution authority is claimed. The Unix socket exists only for the local
fixture protocol because the current process controller deliberately supplies
null stdin. No production stdin or executable-path exception was introduced.
AGD-020 owns the authenticated AR/agentd composition using this controlled process.
No live cluster or vendor harness was needed for this test-only change.
