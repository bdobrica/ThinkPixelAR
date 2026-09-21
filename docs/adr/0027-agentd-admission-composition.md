# ADR-0027: Agentd admission composition

Status: Accepted 2026-09-21

## Context

The authenticated channel, issuer and durable registry deliberately establish
different facts (ADRs 0024–0026). A registry match must not be mistaken for current
Run authority or verified infrastructure. Their application composition must also
prevent a policy/handshake result from changing the persisted Sandbox identity.

## Decision

`agentdadmission.Service` implements the transport Authorizer and credential
authority ports. It loads current compute intent by certificate-derived tenant
and SandboxBinding, then independently matches Attempt, Session, Execution and
generation. Released/stale intent and expired compute deadlines fail closed.

A mandatory AdmissionPolicy supplies current bounded authority, revocation/rate
checks and immutable materialization expectations. It receives persisted compute
intent, never Hello or sandbox-supplied identity. All expected binding fields must
match that intent, and the compatibility helper validates the expectation tuple
before bootstrap consumption. Returned expectations are privately copied.

Except during pre-creation bootstrap issuance, the service queries the provider
and requires the exact persisted reference, verified Ready/Active state, and
matching image, architecture, isolation class, network class and attachment.
Provider observations never grant authority. Calls remain outside SQL transactions.

Credential authorization reads the durable registry version. Commit reloads
current authorization and requires the same version; registration receives the
freshly narrowed deadlines, while the original signing grant also remains an
upper bound. Renewal requires the current peer and accepted connection epoch.

Stream admission checks policy/provider facts before consuming bootstrap, bounds
the connection by authority and certificate expiry, then rechecks expectations,
authority and durable connection state after consumption. A failed recheck closes
only that epoch; the consumed proof is never restored. Close is idempotent and
uses a separate five-second cleanup context even if the stream context expired.

Before frame delivery, the service checks complete binding/connection/epoch,
reloads current compute and authority, reobserves provider state and rechecks the
durable connection. A supplied frame deadline cannot exceed current authority.
A mandatory FramePolicy then checks registered payload semantics, direction,
sequence/replay and durable operation idempotency. There is no permit-all default.
This layer neither invents an execution command handler nor interprets sandbox
observations as lifecycle truth. Failures expose one fixed admission error.

## Consequences

The existing transport and issuer can now share one application composition over
the PostgreSQL binding and credential stores. External authority/materialization
and frame-policy adapters are still required. This checkpoint does not provide
their production implementation, protected Secret delivery/cleanup or rotation
stream handling. AGD-005 remains open and the binary remains dormant.

Focused tests exercise scope/provider/policy rejection, authority changes during
consumption, private expectation/frame copies and stale signing grants. A real
PostgreSQL test composes the service with persisted bindings and credential
records; only external provider/policy observations are fixtures. This does not
claim a deployed end-to-end transport or live Secret lifecycle qualification.

See [evidence](../evidence/agd-005-admission.md).
