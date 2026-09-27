# SES-005 durable replacement coordination

Date: 2026-09-27. Scope: application coordinator and PostgreSQL operation boundary;
SES-005 remains open for concrete infrastructure composition.

Implemented:

- Exact SES-004 checkpoint/runtime/head validation before allocating anything;
  current authorization on replay and runtime/retention policy before forward work.
- Migration 31 journals immutable candidate, bootstrap and attachment identities,
  exact manifest/runtime inputs, bounded deadline, result and cleanup state with
  tenant RLS and one unresolved candidate per Session.
- Transactional single-writer reservation, event/outbox and optimistic fencing;
  provider reconstruction occurs outside the transaction.
- Readiness publication requires independent trusted verification and revalidates
  the checkpoint, policy and fences. No Execution/grant/credential is minted.
- Stable retries after provider response loss, historical result replay, failure
  degradation, close-race fencing and durable idempotent cleanup.

Verification uses real PostgreSQL and signed checkpoint fixtures, with explicit
policy, storage, provider and readiness fixtures. Provider fixtures simulate
creation followed by response loss, deterministic lookup and failed cleanup;
they do not launch Kubernetes, restore files or bootstrap agentd.

Checks passed against a disposable PostgreSQL database with all 31 migrations:

```sh
THINKPIXELAR_DATABASE_URL="$SES005_DATABASE_URL" go run ./cmd/migrate up
THINKPIXELAR_TEST_DATABASE_URL="$SES005_DATABASE_URL" go test -race \
  ./internal/adapters/postgres ./internal/app/session ./internal/app/checkpoint \
  ./internal/adapters/postgres/migrations \
  -run 'TestSessionResume|TestResumeRequest|TestSessionSuspend|TestCheckpointRestore|TestCheckpointPublication|TestRestore|TestManifest|TestLoad' -count=1
go vet ./internal/app/session ./internal/adapters/postgres
```

Tests cover READY/IDLE restoration, old-compute exclusion, concurrent retries,
restart via a replacement service instance, no Execution issuance, disclosure,
changed request/version/tenant/checkpoint rejection, invalid readiness, competing
operations, invalidated checkpoint after allocation, admission exclusion,
concurrent close, cleanup outage/retry and final-outbox rollback at preparation
and publication.

Required composition remains concrete `ResumeMaterializer` and trusted readiness,
current policy and storage/key/compatibility adapters, worker/API wiring, actual
harness restoration/credential isolation, and attachment handoff to the next
freshly authorized Execution. A failed candidate's physical absence must be proven
before cleanup confirmation; uncertain outcomes remain pending. No live CSI,
Kubernetes replacement, process restoration or full cold-resume demo is claimed.
