# KAS-017 — Restart-safe acquisition/release reconciliation

Date: 2026-09-20.

Implemented PostgreSQL intent loading and fenced observation writes, atomic
acquisition/release queueing, exact cleanup confirmation, filtered bounded claims,
lease-aware completion/rescheduling and the application batch worker. Durable
choices are in [ADR-0017](../adr/0017-durable-compute-monitoring.md).

Verification uses the separate local `thinkpixelar_kas010` PostgreSQL database;
the older retained database and its historical migration checksums remain untouched.

- Six concurrent claimers elect exactly one worker.
- A restarted adapter reclaims expired work with the same identity and higher fence.
- Repeated completion/rescheduling and stale observation versions are rejected.
- Cancellation during a provider call prevents the old observation from committing.
- Tenant-scoped claims do not expose another tenant's work.
- Release intent requires exact cleanup evidence; replay remains valid after cancellation.
- Confirmed release commits a cleanup tombstone and completes the one monitoring job.
- Worker tests prove READY continues monitoring and only confirmed absence completes it.

Commands: real-database `go test -race ./internal/adapters/postgres/...
./internal/app/reconciliation/... -count=1` with the explicit test database URL;
`make verify`. Tests without that URL do not count as live PostgreSQL evidence.
Service/authority composition and full Session checkpoint/restore remain later
phase work; no synthetic authority adapter is installed in production composition.
