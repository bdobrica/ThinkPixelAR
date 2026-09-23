# AGD-020 integrated application acceptance

Date: 2026-09-23. Implementation commit: `9531d1b`.
Result: **PASS**, 9.83 seconds (10.884 seconds including race-test
overhead). Test: `TestAgentdIntegratedAcceptance` in
`internal/adapters/postgres/agentd_acceptance_linux_test.go`.

## Executed scenario

One test joined the production agentd executable, a real controlled child, real
loopback mTLS, the AR stream handler, concrete LocalAuthority admission/frame
policies, credential registry, bootstrap delivery journal and PostgreSQL 18.
The disposable database was migrated through 0021. The rebuilt local agentd image
was resolved before launch to
`sha256:e5432d63956a7eef0198ed8fdf14e1ab2ae3886f204be9ad2d7eaf60c6f92c97`.

The container used the image's unprivileged user and real agentd entrypoint,
read-only root/bootstrap/harness mounts, dropped capabilities, no-new-privileges,
and finite CPU/memory/PID limits. Linux host networking connected it to the local
TLS listener. This networking choice is test infrastructure, not isolation evidence.

1. Materialize bootstrap through the existing issuance/delivery coordinator.
   Assert that the durable Secret UID exists before writing the projected bundle.
2. Load that protected bundle in the actual agentd binary, authenticate over mTLS,
   consume the one-time proof, rotate to a registered session certificate and
   reconnect before dispatch, using the same policy path as the hosted listener.
3. Dispatch START, observe RUNNING and captured stdout, and verify the controlled
   harness's exact `fixture.v1` handshake. The probe runs as the container's same
   unprivileged user because its Unix socket is private. Production capture
   suppresses the unregistered payload to `[REDACTED]`; no sanitizer bypass is used.
4. Dispatch STATUS and INTERRUPT; require durable acknowledgements and EXITED.
5. Reconstruct policy access, verify retained outcomes, close the stream and
   reconnect under new PostgreSQL connection epochs.
6. Try the original START operation on the replacement stream. The concrete
   policy rejects Send before delivery. Check an old-epoch command against the
   concrete durable frame policy and the stream boundary; both reject it. This
   probe uses a fresh operation ID so duplicate detection cannot mask a broken
   epoch check; the new epoch must also be strictly greater than the old one.
7. Reconnect again and dispatch a fresh STATUS. Assert exactly four durable
   command rows (START, STATUS, INTERRUPT, final STATUS), all acknowledged; a
   RUNNING report after reconnect is rejected. Rejected attempts create no
   additional command and cannot restart the interrupted harness.
8. Run the tenant bootstrap cleanup worker, require the delivery journal's cleaned
   state and Secret absence, send SIGTERM, require container exit 0 within its
   five-second stop grace and an authenticated final observation, then revoke the
   materialization and prove admission is denied. Remove the disposable container
   and temporary mounts, including the generated credentials.

## Defect found and fixed

The first integrated run reached interrupt but left the post-reconnect STATUS
PENDING. Every successful command attempted to attach process capture. Disconnect
had already abandoned that capture, so reading it on STATUS closed the stream
before the acknowledgement was durably recorded.

The dispatcher now attaches capture only for START/RESTART. Status and stop on a
new stream do not resurrect abandoned output. This implements the existing
ADR-0039 disconnect semantics without changing authority, replay policy, schemas
or dependencies. The complete scenario passed after rebuilding the image.

## Reproduce

Use a disposable migrated PostgreSQL database and set
`THINKPIXELAR_TEST_DATABASE_URL` outside source control. From the repository root:

```sh
docker build --file Dockerfile.agentd --tag thinkpixel-agentd:development .
THINKPIXELAR_TEST_AGENTD_IMAGE=thinkpixel-agentd:development \
  go test -race ./internal/adapters/postgres \
  -run '^TestAgentdIntegratedAcceptance$' -count=1 -v
```

The opt-in test requires Linux, a local Docker engine sharing the host network and
filesystem, and an image matching the host architecture. It builds the separate
test harness and mounts it into the container; no fixture is added to the agentd
image. Normal `make verify` skips this opt-in test when its image variable is
absent. Supplying the image without the database variable fails rather than skips.

Focused process/harness race tests and all existing PostgreSQL agentd regressions
passed. `make verify` passed protocol/OpenAPI drift checks, repository hygiene,
formatting, vet/staticcheck, unit/race tests, vulnerability and dependency/license
checks, and binary builds. The independent stale-operation probe was then added
to the test and the complete opt-in scenario rerun. The disposable database and
test containers were removed; temporary credentials and mounts were removed by
test cleanup. No live homelab resource was changed.

## Scope and remaining work

The initial admitted Session/Execution/Attempt is seeded by the existing database
fixture. There is no new Session API or grant issuer. Kubernetes Secret operations
use the narrow API double; effective infrastructure readiness uses the provider
fixture. Those fixtures are supplied only as external provider dependencies:
neither replaces the concrete admission/frame policy or PostgreSQL outcomes.

AR's real listener, admission service and handler run inside the test process;
this does not launch the complete `thinkpixelar` host binary with its homelab
receipt reader. No fresh Kata/network/workspace qualification, Kubernetes sandbox
deletion or replacement Attempt is claimed. This is one integrated application
acceptance run, not a combination of separate component results. Trusted homelab
evidence publication and live provider acceptance remain deployment qualification
work. Durable safe-replacement admission remains Phase 6 recovery work
(REC-002–006); ambiguous outcomes stay pending rather than being blindly replayed.
Existing ADRs 0039–0043 govern the tested behavior; no new architectural decision
is introduced.

## AGD-020 closure

AGD-020 is closed on 2026-09-23 for the initial runnable application path, using
the integrated result above. This closure follows the requested RC/demo scope;
it does not certify the live provider or automatic replacement. Admission still
requires trusted current infrastructure evidence. AGD-019 remains a separate
Phase 4 evidence/closure review.

Closure verification on 2026-09-23: `make verify` **PASS** (exit 0), including
protocol/OpenAPI drift, repository hygiene, formatting, vet/staticcheck,
unit/race tests, vulnerability scanning (no vulnerabilities found),
dependency/license checks and binary builds. This rerun includes the final
independent stale-operation assertion in the source at `9531d1b`. The opt-in
Docker/PostgreSQL scenario was not rerun by this gate; its executed result is
the separately recorded 9.83-second acceptance run above. Closure changes only
documentation; existing unrelated working-tree edits were excluded from the commit.
