# AGD-005 — binding-isolation acceptance

Completed 2026-09-22. This closes the named requirement: one sandbox cannot
authenticate as another SandboxBinding. It does not claim runnable Phase 4,
production policy composition, a live Kubernetes handshake or platform RC readiness.

## Scope review

AGD-005 had accumulated the remaining credential lifecycle as prerequisites.
The binding-isolation implementation already exists in ADRs 0024–0029. This
closure checks that boundary and assigns remaining runnable integration to
AGD-020, with rotation/reconnect in AGD-013. Both remain Phase 4 exit requirements.
The historical open-status statements in checkpoint ADRs/evidence describe their
implementation dates; none of their security decisions is weakened or superseded.
No new architecture, abstraction, dependency or production behavior is introduced.

## Acceptance evidence

| Requirement | Executable evidence |
| --- | --- |
| Identity comes from the verified certificate, not Hello | gRPC real TCP/mTLS tests reject invalid clients, wrong sandbox identity, invalid trust/EKU/time and mismatched admission/handshake state. |
| Current persisted binding must match | Admission tests reject tenant, SandboxBinding, Attempt, Session, Execution and generation substitutions, stale compute and invalid provider/authority facts. |
| Bad credentials cannot consume another binding's bootstrap | Expanded `TestAgentdAdmissionWithDurableRegistry` rejects tenant/sandbox/Attempt substitutions, wrong certificate digest/proof and expired peer before successfully admitting the original peer/proof. |
| Accepted identity cannot change through frames | The same database-backed test rejects changed sandbox/Attempt and connection epoch before frame delivery; authority revocation also blocks delivery. |
| Bootstrap cannot be replayed across processes | `TestAgentdCredentialRegistryIsolationAndReplay` checks one winner in concurrent consumption, replay rejection using a new registry instance, stale epochs and tenant RLS; admission also rejects replay after acceptance. |
| Cancellation fences credential issuance | `TestAgentdCredentialRegistrationFencesCancellation` checks the persisted cancellation fence. |

The database tests use real PostgreSQL, migrations, aggregate/binding rows,
credential records and connection epochs. Provider observations and external
authority/frame semantics are explicit fixtures. The mTLS tests exercise real
loopback TCP with ephemeral certificates and a fixture authorizer; these are
complementary component/composition checks, not one deployed end-to-end path.

## Validation

Passed focused race tests:

```sh
go test -race ./internal/app/agentdadmission ./internal/adapters/sandboxtransport/... ./internal/app/agentd
# Set THINKPIXELAR_DATABASE_URL and THINKPIXELAR_TEST_DATABASE_URL to an isolated local test database.
go run ./cmd/migrate up
go test -race ./internal/adapters/postgres -run '^TestAgentd(AdmissionWithDurableRegistry|CredentialRegistryIsolationAndReplay|CredentialRegistrationFencesCancellation)$' -count=1 -v
```

The existing development database lacked `sessions.recovery_state`; its explicit
migration attempt failed. Verification instead used a fresh disposable database
in the existing local PostgreSQL container, successfully applied the complete
migration chain and passed all three tests (including the expanded subtests).
No attempt was made to repair unrelated development database drift.

`make verify` passed: generated-artifact checks, hygiene, supported versions,
formatting, static analysis, unit/race tests, vulnerability/dependency checks,
binary builds and OpenAPI checks. Staged whitespace/hygiene and changed Markdown
local-link checks passed. The disposable database was removed after testing.

Earlier supporting evidence: [transport](agd-003-transport.md),
[registry](agd-005-registry.md), [admission](agd-005-admission.md),
[Secret operations](agd-005-bootstrap-secrets.md),
[protected loading](agd-005-credential-loading.md).

Next implementation is bounded process control (AGD-006, with AGD-017's
deterministic child), then capture/status/signals/shutdown and the runnable
composition. The demo track in PLAN.md prioritizes a Codex turn, Session API,
durable suspend/delete/resume, LLMGW, AG and TG in that order.
