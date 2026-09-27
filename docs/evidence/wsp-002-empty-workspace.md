# WSP-002: empty Workspace initialization

Implemented the internal reconciliation service, PostgreSQL empty reservation,
Kubernetes initialization consumer, durable completion journal and atomic
generation-0 publication. Migration 27 adds immutable initializer identity,
specification digest and completion evidence to the storage journal.

The initialization Pod mounts the WSP-001 claims at `/workspace` and `/state`.
It can trigger delayed binding, verifies empty writable roots, synchronizes, and
terminates. Success is saved before exact Pod deletion; readiness waits for its
absence. Retries reuse identities and never erase or reinitialize existing data.
No PVC is owned by or deleted with the Pod.

Verification:

- Focused Kubernetes API fixture race tests: delayed binding, create response loss,
  adapter restart, exact Pod cleanup, retained claims, wrong/missing UID, failed
  initialization, changed mount/specification, sidecar/privilege injection and
  missing qualification.
- Executed the initialization shell command on temporary directories; empty roots
  succeed and an existing newline-named file is rejected and preserved.
- Real isolated PostgreSQL database migrated through version 27. The application
  service and real provider adapter ran against an HTTP Kubernetes API fixture:
  concurrent workers used one Pod, restart replay retained proof, rollback before
  publication left no generation, and successful replay published exactly one
  generation 0. Changed requests, cross-tenant lookup, denied admission and journal
  UID replacement were rejected. WSP-001 journal regression also passed.
- Focused migration checks and Go vet passed.

Reproduce the database tests after applying migrations to a disposable database:

```sh
THINKPIXELAR_TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -race \
  ./internal/adapters/postgres \
  -run 'TestEmptyWorkspaceReconciliation|TestWorkspaceStorageJournal' -count=1
go test -race ./internal/adapters/workspace/kubernetes
go test ./internal/adapters/postgres/migrations
go vet ./internal/adapters/workspace/kubernetes ./internal/adapters/postgres \
  ./internal/ports/workspace ./internal/app/workspace
```

These tests do not qualify a live CSI implementation or prove node-replacement
portability. No cluster deployment or live mount test was performed. WSP-001 found
no CSI driver in the current homelab. Trusted admission/qualification and outbox
worker wiring remain necessary for executable composition. The initializer image
must provide `/bin/sh`, `find` with depth/quit support, and `sync`; roots containing
provider-created entries such as `lost+found` are unsupported. Subsequent fenced
Execution attachment, checkpoint/snapshot publication and failed-initializer
cleanup are separate workflows. Empty generation 0 is not a restorable snapshot.
