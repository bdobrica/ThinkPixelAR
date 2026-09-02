# Database migrations

Versioned PostgreSQL migrations are embedded from
`internal/adapters/postgres/migrations/sql` into `cmd/migrate`. They run only
through `cmd/migrate` (locally, `make migrate ARGS="up"`) under
the dedicated DDL identity required by the persistence contract. The API
service does not import or invoke migration code.

Released migration files are immutable. The command serializes deployments with
a PostgreSQL advisory lock, verifies SHA-256 checksums against the
`schema_migrations` ledger, and applies each pending file in its own transaction.
There are deliberately no automatic down migrations; schema rollback follows
the expand–migrate–contract policy in the persistence contract.
