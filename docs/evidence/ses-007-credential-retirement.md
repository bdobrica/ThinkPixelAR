# SES-007 credential retirement

Verified 2026-09-27 against a disposable PostgreSQL database with all 31 existing
migrations and the pinned Codex 0.155.0 amd64 binary.

The coordinator now rejects old connection projections and bootstrap deliveries
that are neither confirmed cleaned nor expired. Checks run before allocation,
on pending retries and before readiness publication, with the Session locked.
Old released bindings remain inadmissible to the credential registry after resume.
No credentials are issued or copied by this change; no schema/API changes.

Passed:

```sh
THINKPIXELAR_DATABASE_URL="$SES007_DATABASE_URL" go run ./cmd/migrate up
THINKPIXELAR_TEST_DATABASE_URL="$SES007_DATABASE_URL" go test -race \
  ./internal/adapters/postgres -run 'TestSessionResume|TestSessionSuspend' -count=1
THINKPIXELAR_TEST_CODEX_BINARY="$CODEX_BINARY" go test -race \
  ./internal/app/agentd \
  -run 'TestPinnedCodexRestoredSupervisor|TestCodexSupervisedRestore' -count=1
go vet ./internal/adapters/postgres
```

PostgreSQL tests cover queued cleanup without partial allocation, confirmed
cleanup, expired delivery, differing certificate/delivery deadlines, stale
connection state discovered after allocation, pending retry, and old identity
issuance/stream/bootstrap denial after IDLE publication. A separate case retains
an unexpired certificate with a matching known proof and latest-digest registry
entry, verifying that a valid old bootstrap cannot regain admission. Existing
resume replay, failure cleanup and transactional rollback tests also pass.

The real Codex test starts and stops the original process, removes its home,
restores the same conversation in a fresh supervised process, and verifies that
old credential files and inherited environment are absent. It uses a local model
fixture and does not exercise gateway credentials.

Provider Secret deletion is represented by a trusted cleanup fixture; it is not
a live Kubernetes test. AG/gateway revocation, credential-canary validation of
checkpoint content, and executable Session worker/readiness composition remain
required. SES-007 stays open until those paths are integrated and demonstrated.
