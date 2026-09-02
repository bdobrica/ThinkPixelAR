# DB-001 PostgreSQL driver review

Status: implementation evidence for DB-001.

ThinkPixelAR adds `github.com/jackc/pgx/v5` version `v5.7.6` as an external
runtime library owned by the PostgreSQL adapter and explicit migration command.
It supplies the pure-Go PostgreSQL `database/sql` driver needed to execute
embedded, transactional migrations. The Go standard library has no PostgreSQL
driver, while invoking an external `psql` process would add an undeclared
runtime dependency and provide weaker transaction and error control.

The selected release comes from the maintained `jackc/pgx` upstream project
under the MIT license. Its types remain inside the PostgreSQL adapter and
migration command boundary. The module checksum and transitive dependency
inventory are recorded by `go.sum` and the repository dependency/license gate.
