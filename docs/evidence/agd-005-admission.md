# AGD-005 checkpoint — trusted admission composition

Implemented 2026-09-21; [ADR-0027](../adr/0027-agentd-admission-composition.md).

The application service composes compute intent, verified provider observations,
bounded authority policy, immutable handshake expectations and the durable
credential registry. It implements both transport and credential authority ports
without supplying a permissive default policy.

## Verification

- Scope mismatch for tenant, SandboxBinding, Attempt, Session, Execution and
  generation is rejected before bootstrap consumption.
- Stale/released compute, expired/widened authority, substituted provider,
  unready/unverified infrastructure, image/network mismatch and invalid handshake
  expectations fail closed.
- Revocation, fencing, changed expectations or a shortened deadline during
  consumption close the allocated epoch and return no accepted lease.
- Frames require current binding/connection/epoch, registry, provider and policy
  checks before semantic delivery. Private copies prevent callback/caller
  mutations from changing accepted expectations or input frames.
- Credential commit revalidates authority and registry version after signing;
  narrowed authority cannot be widened by the earlier signing grant.
- Real local PostgreSQL composition registers a credential, rejects cross-Attempt
  admission, consumes bootstrap, accepts a current frame, rejects revoked delivery
  and rejects bootstrap replay. External provider/authority/frame policy are
  explicit fixtures; the binding/fence and credential registry are actual SQL.

Commands:

```sh
go test -race ./internal/app/agentdadmission
# Set THINKPIXELAR_TEST_DATABASE_URL to an isolated, migrated local test database.
go test -race ./internal/adapters/postgres -run TestAgentdAdmissionWithDurableRegistry -count=1
make verify
```

Focused admission race tests and the real PostgreSQL composition test passed.
`make verify` passed: generated drift, hygiene, versions, formatting, static
analysis, repository-wide unit/race tests, vulnerability scan (no vulnerabilities),
license checks, binary builds and OpenAPI checks. Staged whitespace and changed
local Markdown links also passed. No Kubernetes changes or new dependencies were
needed. AGD-005 remains open for concrete authority/expectation
and frame-policy adapters, protected Secret delivery/cleanup and rotation wiring.
The configured binary still does not start a transport listener or harness.
