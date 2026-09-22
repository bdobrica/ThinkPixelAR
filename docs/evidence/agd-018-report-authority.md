# AGD-018 sandbox reports and AR authority

Date: 2026-09-22

## Scope

Regression tests for the accepted boundaries in
[ADR-0027](../adr/0027-agentd-admission-composition.md),
[ADR-0032](../adr/0032-agentd-status-heartbeat.md) and the
[agentd contract](../contracts/agentd.md). No production behavior, wire contract,
dependency or architectural decision changes.

The admission suite submits each valid process-state claim (ABSENT, STARTING,
RUNNING, STOPPING, EXITED, FAILED) on an already admitted connection after changing
trusted facts independently of the report:

- obsolete current-Attempt designation or released compute intent;
- advanced Session generation or changed Execution;
- unknown provider state, lost effective verification or replaced provider identity;
- revoked policy or expired compute deadline;
- old connection epoch or forged Attempt binding.

Every contradictory/stale combination fails before the semantic frame policy.
A separate policy-denial case verifies that admission propagates rejection.
Positive controls show that each syntactically valid report reaches policy while
leaving compute intent, authority expectations/deadlines, provider facts, peer,
connection and registry version unchanged. A write-spy also rejects and counts
any attempted compute mutation; no such writes occur. Incoming frames remain
unchanged despite the existing policy fixture mutating its private copy.

Aggregate tests verify that accepted liveness observations leave lifecycle and
current designation unchanged, terminal Attempts reject both kinds of heartbeat
without any aggregate mutation, and future/pre-creation/regressing timestamps
or stale optimistic versions cannot change the aggregate. A heartbeat cannot
publish success, revive a replaced Attempt or override terminal results.

## Verification

- `go test -race ./internal/app/agentdadmission ./internal/domain/attempt`: passed.
- `make verify`: passed.

## Limits and integration obligation

Compute/provider/authority/registry dependencies are explicit fixtures in the
admission tests; these are not live PostgreSQL, Kubernetes or end-to-end binary
checks. The semantic-policy fixture deliberately allows valid reports in the
positive controls. This proves that admission does not itself promote claims to
truth, not that a production report reducer exists.

AGD-020 still owns concrete command/report semantics, durable reconciliation,
contradiction handling and authenticated dispatch. It must reject impossible
readiness/completion/checkpoint claims against durable operation/process binding
and independent evidence before mutations, and implement the contract's bounded
security telemetry/compromise response. Phase 4 cannot exit on these tests alone.
No permissive default policy or alternate source of authority was added.
