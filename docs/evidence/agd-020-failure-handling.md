# AGD-020 failure-handling composition

Date: 2026-09-22. Decision: [ADR-0043](../adr/0043-agentd-failure-fencing-and-final-observations.md).

## Implemented scope

- The binary separates command cancellation from the bounded final-report window.
  Local stop precedes a closed authenticated stop hint; failure to deliver never
  grants offline work or changes command outcomes.
- AR's concrete frame policy accepts only the closed stop schema under current
  binding, authority, connection epoch and replay sequencing. The host logs a
  bounded hint and acknowledges receipt. No lifecycle reducer trusts it.
- The configured host sweeps latest credential expiry and drains exact saved
  release intents. Expiry, Session degradation, recovery work, event/outbox and
  cleanup intent are atomic. Existing provider fencing handles deletion retries.
- Ambiguous command outcomes survive final reports and expiry. Recovery work stays
  pending until safe replacement admission exists; no automatic new Attempt or
  command replay is claimed.

## Verification

Focused real-process/mTLS race tests cover abrupt stream loss and orderly local
cancellation, stopping within five seconds, final-report delivery after managed
process exit, rotation/reconnect and preservation of command replay guards.
The orderly runnable-supervisor test keeps AR alive while cancelling agentd and
requires an authenticated final observation within the shutdown window.

PostgreSQL tests ran against a disposable database migrated through 0021:
`go test -race ./internal/adapters/postgres -run '^TestAgentd' -count=1`.
The new expiry test uses real finite certificate records and database time. It
proves no premature fencing, successful renewal surviving old-certificate expiry,
idempotent recovery/cleanup creation, stale reconnect/report rejection and a
PENDING command remaining PENDING after an admitted final report and fencing.
A restarted store and the real compute reconciler drain the saved release operation
against a provider fixture and confirm absence without completing recovery work.
Existing current-epoch, tenant/Attempt binding and old-close regressions also pass.

The local OCI `make agentd-image-smoke` passed, including actual agentd PID 1,
privilege restrictions and SIGTERM exit within the container stop budget.
This uses an unavailable transport endpoint and ephemeral test certificates; it
does not establish live provider admission or authenticated reporting in Kata.

Final `make verify` passed: protocol/OpenAPI drift, repository hygiene and its
self-test, supported versions, formatting, vet/staticcheck, unit/race tests,
vulnerability and dependency/license checks, and all binary builds. The final
image smoke passed again after permanent cancellation was consolidated onto one
Shutdown operation. Focused race tests were rerun after that change. The final
PostgreSQL expiry/cleanup regression passed with the restart-drain assertion;
the disposable test database was removed. Staged whitespace and repository
hygiene checks passed. No dependency or generated artifact changes were needed.

## Remaining acceptance

No live homelab changes, host evidence publisher deployment, physical sandbox
replacement, safe-retry classification or new-Attempt admission were tested or
implemented here. Pending recovery is intentional for ambiguous work, as selected
by the user. AGD-020/019 remain open for durable safe-replacement admission and the
integrated controlled-process exchange with trusted fresh homelab evidence.
