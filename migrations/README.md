# Database migrations

Versioned PostgreSQL migrations will be added here by DB-001 in Phase 2. They
will run only through `cmd/migrate` (locally, `make migrate ARGS="..."`) under
the dedicated DDL identity required by the persistence contract. The API
service does not import or invoke migration code.

Released migration files are immutable. Their ordering, checksums, deployment
lock, and transactional behavior will be implemented and tested with the first
schema rather than implied by this empty directory.
