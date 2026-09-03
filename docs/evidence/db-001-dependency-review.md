# DB-001 PostgreSQL driver review

Status: implementation evidence for DB-001.

ThinkPixelAR uses `github.com/jackc/pgx/v5` version `v5.9.2` as an external
runtime library owned by the PostgreSQL adapter and explicit migration command.
It supplies the pure-Go PostgreSQL `database/sql` driver needed to execute
embedded, transactional migrations. The Go standard library has no PostgreSQL
driver, while invoking an external `psql` process would add an undeclared
runtime dependency and provide weaker transaction and error control.

The selected release comes from the maintained `jackc/pgx` upstream project
under the MIT license. DB-023 advanced the original `v5.7.6` selection to
`v5.9.2`, the first upstream release containing the fix for GO-2026-5004
(GHSA-j88v-2chj-qfwx), after the pinned vulnerability gate identified a
reachable affected path. This is a same-major security update and does not
change the adapter boundary.

The driver's types remain inside the PostgreSQL adapter and migration command
boundary. The module checksum and transitive dependency inventory are recorded
by `go.sum` and the repository dependency/license gate.
