# ADR-0028: Agentd bootstrap Secret projection

Status: Accepted 2026-09-21

## Context

ADR-0002 requires read-only ephemeral bootstrap delivery and cleanup after
exchange, failure or expiry. ADRs 0025–0027 implement issuance, registration and
admission composition but not the Kubernetes Secret operations. Ambiguous creates
and name reuse must not cause adoption or deletion of another object's contents.

## Decision

The `sandboxtransport/bootstrap` adapter owns bounded immutable Secret publication,
exact lookup and deletion in one explicitly configured namespace/trust domain.
It uses the existing dynamic Kubernetes client; it adds no dependency and exposes
no Kubernetes API or discovery to the sandbox. It never lists, patches or updates
Secrets. Ordinary Go formatting of its material object is redacted.

Publication requires the exact registered, unconsumed bootstrap credential.
`AgentdCredentials.CheckBootstrap` supplies this check from PostgreSQL and
revalidates the current aggregate fence. The adapter checks registration before
and after creation, and before and after lookup. This does not replace current
Run authority checks in trusted materialization/admission.

The closed bundle contains `config.json`, `client.crt`, `client.key`,
`server-ca.crt`, `bootstrap.proof`, `challenge.bin` and `trust-domain`. Config uses
the existing closed agentd decoder. The key must match the leaf; certificate
digest, issuer digest, exact URI identity, clientAuth usage and validity must match
the registered bootstrap. Proof is exactly 32 bytes and must match its registered
hash. Challenge is exactly 32 bytes. Certificates/keys/trust/config have explicit
byte ceilings; server trust contains at most four current CA certificates, no
trailing data, and cannot validate the client identity's chain.

Secret name is deterministic from credential ID. Ownership labels contain the
tenant, SandboxBinding, Attempt and credential IDs. Annotations record expiry and
a deterministic length-prefixed bundle digest. The Secret is immutable/Opaque,
has no owner reference to a not-yet-created Sandbox, and must have no finalizers.
The existing sandbox template projects it read-only with mode 0440. No material
is copied into Workspace, vendor state, process environment or source files.

`Plan` returns non-secret target metadata before any external write. Trusted
composition must durably persist that plan and cleanup intent before `Publish`.
Publication creates or verifies identical existing contents; it never overwrites
a conflicting object. The returned UID must be persisted before projection.
`Resolve` accepts only the exact persisted UID and returns a name, not contents.

`Recover` resolves an ambiguous create only when the planned name, ownership and
bundle digest still match. It works after consumption/expiry for cleanup only;
projection still requires pending registration. Missing objects have a distinct
absence result. Deletion checks ownership/content and uses both UID and observed
resourceVersion preconditions. A replacement or concurrent mutation fails closed.
Cleanup does not require still-valid execution authority. If publication loses
its registry fence, it attempts exact deletion and returns any known reference
with the error for durable cleanup; it never restores bootstrap usability.

## Consequences

The Kubernetes operations and concrete database publication gate are implemented.
AGD-005 remains open for durable plan/UID/cleanup scheduling composition, sandbox
credential loading, concrete authority/expectation/frame policy and rotation
wiring. No listener or production materialization path is enabled yet.

Tests use ephemeral in-memory certificates and a narrow Kubernetes API double
that enforces deletion preconditions. Real PostgreSQL tests validate the pending
publication gate and rejection after consumption. These are local component
checks, not a live Kubernetes Secret or complete bootstrap lifecycle qualification.

See [operations](../operations/agentd-bootstrap-secrets.md) and
[evidence](../evidence/agd-005-bootstrap-secrets.md).
