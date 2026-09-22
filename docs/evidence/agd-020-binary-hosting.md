# AGD-020 — executable transport hosting and homelab evidence reader

Date: 2026-09-22. Partial AGD-020 implementation under
[ADR-0042](../adr/0042-agentd-binary-hosting-and-homelab-evidence.md).
AGD-020 and AGD-019 remain open.

## Implemented path

- Agentd's executable no longer waits in `awaiting_transport`. It validates the
  protected complete bundle, finite cutoff and required process/rotation
  capabilities, then hosts the dispatcher, output/heartbeat forwarding, renewal,
  reconnect and bounded local termination.
- AR's explicitly enabled separate listener composes local PostgreSQL policies,
  current compute/provider/credential checks, durable command claims/outcomes,
  dedicated mTLS issuer/server, serialized command/report/rotation handling, and
  tenant cleanup worker. HTTP-only startup stays available without cluster access.
- Operator plans address already admitted bindings and never grant authority.
  Bootstrap credentials rotate before a plan starts. Acknowledged operations are
  skipped; pending/unknown/conflicting/unavailable outcomes stop dispatch. Only an
  explicit not-found result permits attempting a new atomic Send claim.
- A homelab verifier reads private root/AR-owned receipts with at most 30-second
  validity, rejects scope/runtime/image/resource/mount mismatches, and rechecks
  live node/class/namespace/policy/claim/PV identities. The existing complete Pod,
  capability and namespace policy verifiers remain mandatory.
- Nonzero local cutoffs bind to the persisted materialization deadline; disconnect
  and profile/effective-Pod shutdown budgets are checked. Old configuration is
  still decodable, but a missing cutoff cannot run the supervisor.

No protocol generation, public API schema, database migration, new dependency or
cross-component ownership change was required.

## Verification scope

`TestRunnableSupervisorAuthenticatedRotationAndStatus` uses real mTLS and the
actual runnable supervisor with the new AR handler. It verifies bootstrap renewal,
reconnect to a later epoch, authenticated STATUS, heartbeat and cancellation cleanup.
Its authorizer/outcome fixtures are explicitly test-only. It does not start a
harness or substitute for the existing structured-process exchange.

The test exposed an EOF/rotation-installation race when AR closed immediately
after sending ISSUED. A bounded handoff window fixes it; repeated race-enabled
startup tests passed. Missing cutoffs/capabilities and excessive disconnect budgets
are rejected without retaining the bootstrap credentials.

Homelab verifier tests use a narrow Kubernetes HTTP fixture and protected temporary
receipts. They cover successful validation, absence/expiry/future/overlong validity,
Pod/container/binding/node/image/runtime/resource/mount mismatches, missing object
proof, changed live API identities/revisions and cancellation. A separate test
rejects a matching desired/actual blueprint with an unsafe termination grace.
These are verifier behavior tests, not physical homelab qualification.

All agentd PostgreSQL tests passed with race detection on the existing local
PostgreSQL 18 container, using a separately created disposable database with the
21 existing migrations. The disposable database was removed after testing. Coverage includes credential kind lookup, tenant/Attempt
isolation, finite-cutoff materializations, durable claims/outcomes, conflicting
versus absent lookup, bootstrap acceptance/failure/expiry cleanup and revocation.
The final lookup change was then rerun with all policy tests and runnable-startup tests.

Commands used:

```sh
go test -race ./internal/app/agentd ./internal/app/agentdserver \
  ./internal/adapters/sandbox/agentsandbox \
  ./internal/adapters/sandboxtransport/host ./cmd/thinkpixelar
# With THINKPIXELAR_TEST_DATABASE_URL targeting the disposable database:
go test -race ./internal/adapters/postgres -run '^TestAgentd' -count=1
go test -race ./internal/adapters/postgres ./internal/app/agentd \
  -run '^(TestAgentdPolicy|TestRunnableSupervisor)' -count=1
```

## Aggregate, image and architecture checks

`make verify` passed on the final source: protocol/generated-artifact checks,
formatting, vet/Staticcheck, unit/race tests, vulnerability/license/hygiene checks,
all binary builds and OpenAPI validation. No generated contract drift occurred.
Both executables also cross-compiled successfully with `GOOS=linux GOARCH=arm64`
and CGO disabled; this is a build check, not ARM64 runtime qualification.

`make agentd-image-smoke` passed on the local amd64 image. It checks incomplete
transport rejection, then mounts a dynamically generated test-only protected bundle
and verifies the actual supervisor as PID 1: UID/GID, capabilities, seccomp,
no-new-privileges, namespace/mount/device/credential restrictions and syscall denial.
SIGTERM exits successfully. No authority or live endpoint is supplied by the fixture.

This image check also caught configuration equality comparing protobuf-internal
cache fields. Startup now compares the serialized configuration fields instead;
a regression test still rejects changed binding and cutoff values. The first
probe-entrypoint experiment was replaced by the real-PID-1 check rather than
weakening the existing privilege probe. Temporary fixture keys are removed by cleanup.

## Remaining acceptance

The [runbook](../operations/agentd-hosting.md) documents configuration and the
publisher's observation obligations. **Automatic trusted worker evidence collection
is not implemented or deployed by this change.** No homelab context was used, no
worker was modified, and no live secure admission is claimed. A consumer accepting
a synthetic fixture receipt does not establish the truth of real infrastructure.

Still required: deploy a trusted publisher with actual host/network/mount observations,
run the controlled-process exchange with concrete policies/PostgreSQL and live
receipts, complete authenticated final reporting, and exercise fenced sandbox
replacement after unrecoverable credentials/transport loss. Production-size amd64
and encrypted storage remain future qualification, not paid prerequisites here.
