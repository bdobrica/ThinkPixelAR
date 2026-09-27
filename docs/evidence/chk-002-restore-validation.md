# CHK-002 restore validation evidence

Date: 2026-09-27

Implemented the SHA-256/Ed25519 restore validator and an authorized PostgreSQL
entry point selecting immutable committed metadata. Validation checks canonical
JSON/schema, payload/composite roots, current signing-key policy, exact identity
and runtime binding, registered compatibility, Workspace proof and streamed vendor
object digest/size. It returns no manifest on failure and grants no authority.

Verification performed:

- Focused Go race tests for publication and restore passed. Cases include valid
  CHK-001 output, duplicate keys/Unicode/encoding/size limits, signed invalid
  metadata, signature corruption, changed bindings, revoked keys, incompatible
  adapters, object truncation/extra bytes/corruption/missing/read/close failures,
  current authorization denial, tenant/Session isolation and deleting checkpoints.
- Real PostgreSQL tests applied existing migrations through 29 to disposable
  `thinkpixelar_chk002`, published WSP-003 and CHK-001 records, and validated their
  original manifest bytes. A concurrent mutation lock could not acquire the
  checkpoint while external verification was in progress. No new migration.
- `go vet ./internal/app/checkpoint ./internal/adapters/postgres` passed.

Reproduce after migrating a disposable database, with its URL in
`THINKPIXELAR_TEST_DATABASE_URL`:

```sh
go test -race ./internal/adapters/postgres ./internal/app/checkpoint \
  -run 'TestCheckpointRestore|TestCheckpointPublication|TestRestore|TestManifest' -count=1
go vet ./internal/app/checkpoint ./internal/adapters/postgres
```

Tests use ephemeral signing keys, explicit compatibility/Workspace fixtures and
in-memory vendor bytes. Concrete immutable-store verification/retention pins,
credential scanning, key lifecycle configuration and qualified adapter registry
composition remain dependencies. This is not live CSI or Codex cold-restore
qualification. SES-005 must integrate validation before harness startup, record
Session degradation/evidence on integrity failure, retain the exact verified
objects, and recheck current execution authority and fences. Validation itself is
read-only and deliberately cannot authorize or start a harness.
