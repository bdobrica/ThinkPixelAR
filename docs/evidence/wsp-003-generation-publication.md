# WSP-003 — Workspace generation publication

Implemented the internal checkpoint publication port and PostgreSQL adapter with
migration 28. A durable reservation binds the source generation and writer fence;
one transaction publishes the next immutable generation, Workspace head, operation
completion, runtime event and outbox candidate linkage. Exact retries recover the
same result across adapter replacement and later generation advancement. Abort
preserves the previous generation and degrades the Workspace without resuming writes.

Verified against an isolated local PostgreSQL database:

- Concurrent publication retries produce one generation/event/outbox message.
- Restart preserves prepared intent; historical replay returns the original child.
- A forced outbox insertion failure rolls back generation, event, head and journal.
- Cancellation, stale Attempt/attachment, changed source/configuration/epoch,
  changed proof, foreign tenant and denied access prevent publication/disclosure.
- Detached `READY` and current `ATTACHED` boundaries restore their prior states.
- Generation and completed operation metadata reject modification.
- Migrations apply from an empty schema and replay without changing their ledger.
- Focused race tests and Go vet pass.

Reproduce with `THINKPIXELAR_TEST_DATABASE_URL` pointing to a disposable migrated
PostgreSQL database:

```sh
go test -race ./internal/adapters/postgres ./internal/adapters/postgres/migrations ./internal/ports/workspace \
  -run 'TestWorkspaceCheckpoint|TestCheckpointValidation|TestLoadReturns|TestUpFromEmptyPostgreSQL' -count=1
go vet ./internal/adapters/postgres ./internal/ports/workspace ./internal/adapters/postgres/migrations
```

The tests inject trusted authorization and durability verification fixtures; they
do not create real provider snapshots or qualify CSI durability. No live cluster
was modified. WSP-004 supplies provider snapshots; concrete quiescence/verification,
worker composition and signed restorable checkpoint assembly remain separate.
The migration table-list check was also brought up to date with previously added
Execution input/signal and Workspace storage tables so the full chain is checked.
