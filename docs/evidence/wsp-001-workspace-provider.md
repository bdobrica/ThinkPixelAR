# WSP-001: Kubernetes Workspace storage provider

Implemented the provider-neutral create/get/delete port, Kubernetes dynamic-client
adapter, and PostgreSQL durable operation journal (migration 26). The adapter
creates separate Workspace/vendor-state filesystem claims, validates immutable
ownership/configuration and requested/effective storage facts, and records exact
UIDs. It reconciles partial/ambiguous creates using deterministic names and the
same operation. Bound claims that disappear are never silently recreated.
Compute has no ownership reference on these claims.

Deletion requires independently admitted standalone cleanup, checks exact
ownership/UID and uses UID/resourceVersion preconditions. Both claims must be
observed absent before the provider reports `ABSENT`. Metadata/generations and
physical PV disposal remain their respective owners' responsibilities.

Verification performed:

- Race-enabled adapter tests using client-go against an HTTP Kubernetes API
  fixture: create/pending/bound/delete, restart, concurrent replay, partial pair,
  lost response, missing/replaced claim, ownership/configuration/source/access
  drift, qualification/admission failure, and sanitized errors.
- Real PostgreSQL migration and race-enabled journal test: reference commit
  survives failed provider work, adapter recreation, conflicting replay,
  cross-tenant lookup, policy denial, lifecycle exclusion, durable delete intent,
  and database rejection of UID replacement.
- Focused migration checks and Go vet for affected packages.

Reproduce with an isolated migrated PostgreSQL database:

```sh
GOCACHE=/tmp/thinkpixelar-go-cache go test -race ./internal/adapters/workspace/kubernetes ./internal/ports/workspace
# Set THINKPIXELAR_TEST_DATABASE_URL to the isolated database first.
GOCACHE=/tmp/thinkpixelar-go-cache go test -race ./internal/adapters/postgres -run TestWorkspaceStorageJournal -count=1
```

A read-only homelab check on 2026-09-27 found only `local-path`
(`rancher.io/local-path`) and `ar-bounded-scratch-v1`
(`kubernetes.io/no-provisioner`), and no CSIDriver objects. No CSI deployment,
live storage mutation, mounted filesystem, snapshot, or node-replacement test
was performed. This is adapter/database evidence, not live CSI qualification.

Service composition must supply current storage qualification and access/retention
policy, and budget two database connections per concurrent provider operation.
The stock executable is not wired to provision Workspaces yet. Empty filesystem
initialization, generation 0, attachment/mount verification and delayed-binding
orchestration remain WSP-002; snapshot/checkpoint publication remains WSP-003–004.
No new runtime authority, provider credentials in harness state, dependencies,
or public HTTP API were introduced.
