# AGD-020 — local admission and durable replay policies

Date: 2026-09-22. Policy/persistence step complete; AGD-020 remains open.

## Implementation and scope

`postgres.AgentdPolicy` implements AdmissionPolicy, FramePolicy and CommandOutcomes.
Explicit local mode, the local issuer, an approved revision and one immutable
registered materialization record are required. Current aggregate fences, exact
grant/configuration identity, authority expiry, durable epochs and irreversible
revocation gate delivery. Unknown or unresolved command outcomes block further
dispatch. See [ADR-0040](../adr/0040-local-agentd-admission-and-durable-dispatch.md).

Use NewAgentdLocalPolicy with `local`, the trusted revision digest and
`thinkpixelar/local`. After normal Execution admission and SandboxBinding reservation,
Register the exact non-secret agentd configuration/materialization. Pass the same
policy to both policy arguments of agentdadmission.New. Its provider and credential
registry remain mandatory. Do not call FramePolicy as a substitute for that service's
provider/credential checks, or bypass it on Send. Read CommandOutcome on a retry;
PENDING/UNKNOWN requires reconciliation and cannot authorize another Send.

This does not implement LocalAuthority grant issuance, the ThinkPixelAG adapter,
binary wiring, bootstrap cleanup scheduling or sandbox replacement orchestration.
Tests use existing pre-admitted Execution fixtures. No live provider or mTLS exchange
with these policies is claimed; provider facts in the composition test are fixtures.

## Verification

A disposable PostgreSQL database was migrated through 0021. Passed:

```sh
# With THINKPIXELAR_TEST_DATABASE_URL set to the disposable database:
go test -race ./internal/adapters/postgres ./internal/adapters/postgres/migrations -count=1
go test -race ./internal/adapters/postgres -run TestAgentdPolicy -count=3
```

The database tests exercise eight concurrent command claims with one winner,
reconstructed policy instances reading pending and acknowledged outcomes,
conflicting/unrelated acknowledgements, immutable terminal outcomes, unknown-outcome
fencing, wrong binding/handle/digest/schema/deadline, sequence gaps, changed operator
revision/issuer, irreversible revocation, tenant isolation, and durable epoch
replacement. Composition uses the actual admission service and PostgreSQL binding,
credential and policy stores; hostile heartbeat progress is rejected and candidate
RUNNING reports leave the Execution MATERIALIZING. The full adapter regression suite
and migration-from-empty test also passed.

`make verify` passed, including static analysis, unit/race tests, vulnerability and
license checks, builds, Protobuf drift and OpenAPI checks. Staged whitespace,
repository hygiene and local Markdown links passed. The disposable database was
removed after testing; no existing development or homelab database was migrated.
