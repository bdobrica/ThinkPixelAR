# ENG-012 PostgreSQL development dependency

Reviewed 2026-08-31.

| Dependency | Class | Exact artifact | License | Repository-local purpose |
| --- | --- | --- | --- | --- |
| PostgreSQL Docker Official Image | Development runtime service | `postgres:18.6-alpine3.24@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2` | PostgreSQL | A repeatable real PostgreSQL service for local migration and later integration work. |

PostgreSQL is already the accepted authoritative persistence boundary in the
normative persistence contract and PLAN; this dependency does not introduce a
new authority or cross-component database access. The Compose service binds
only to loopback, uses conspicuously development-only credentials, persists in
a named local volume, and has a readiness check. It is not production
packaging or release-qualification evidence.

The image is pinned by the Docker Hub multi-platform index digest in addition
to the exact PostgreSQL and Alpine tag. PostgreSQL 18.6 was the current stable
patch release reviewed from the upstream 2026-08-13 release announcement. The
Docker Official Image metadata identified the tag and digest. No Go database
or migration library is added by ENG-012: DB-001 owns selection of the
migration framework together with its checksum, locking, and transactional
tests.

The `cmd/migrate` skeleton establishes the separate executable boundary and
fails closed for migration actions until DB-001 implements them. The service
binary does not import it and therefore cannot auto-migrate.

Sources:

- <https://www.postgresql.org/about/news/postgresql-186-1711-1615-1519-1424-and-19-beta-3-released-3365/>
- <https://hub.docker.com/_/postgres>
