# ADR-0042: Host authenticated agentd transport with homelab evidence

Status: Accepted 2026-09-22

Supersedes the temporary `awaiting_transport` behavior in ADR-0023. Its protected
bootstrap, credential-exposure and process configuration safeguards remain intact.

## Decision

Host the existing authenticated transport in both executables. Agentd requires
the complete protected bundle, `process-control.v1`, `rotation.v1`, and a future
`control_deadline_unix_ms`. The optional configuration field remains decodable
when absent for compatibility with old configuration tooling; the runnable binary
rejects an absent cutoff. Nonzero cutoffs match the immutable AR materialization
deadline exactly to millisecond precision and participate in its configuration
digest. They can narrow local lifetime, never establish or renew authority.

The disconnect stop budget is strictly below five seconds. For materializations
with a cutoff, AR checks that the supervisor shutdown budget plus a five-second
margin fits the profile's termination grace. Effective verification also checks
that the desired and actual Pod grace agree with that profile. Agentd closes
process admission and terminates children on exit; immutable bootstrap cannot
supply a replacement credential after expiry.

AR enables its separate TLS listener only with explicit operator configuration.
It composes the PostgreSQL LocalAuthority admission/frame policy, current binding
and epoch checks, Agent Sandbox provider, dedicated client issuer, rotation service
and tenant bootstrap cleanup worker. Missing configuration fails startup; missing
or invalid live evidence rejects admission. There is no ThinkPixelAG fallback,
implicit grant issuer, database migration, bootstrap reissuance or plaintext port.

Until the Session/Execution API exists, the host accepts a bounded, explicit
operator command plan for already admitted bindings. The plan supplies immutable
configuration/handle/operation identities and finite deadlines, not new authority.
The host waits for a session credential before dispatching the plan. Durable
acknowledged outcomes are skipped on reconnect; ambiguous outcomes stop the plan.
Send still atomically claims every new operation before network delivery. A plan
never restarts work merely because a stream or AR process restarted.

One stream owner serializes commands, heartbeat/output acknowledgements and
credential replies. After sending rotation material, AR allows up to five seconds
for agentd to install it and close the predecessor stream; immediate server closure
can race installation with EOF. Private-key reply buffers are cleared after Send.
Reports remain untrusted hints. The initial executable consumes them without
publishing canonical events; harness normalization remains Phase 5. Unregistered
output content remains suppressed by the existing capture policy.

## Homelab evidence boundary

Provide a concrete protected-file evidence reader for the existing ARM64 bounded
runtime lane. It composes with the existing secure Pod comparison, capability
discovery and namespace policy checks. Each receipt binds the exact acquisition
request, scope, provider reference, Pod/container, node, RuntimeClass, manifest,
qualified artifact fingerprints, resource observations and mounted workspace.
It references retained trusted network, mount/fencing and resource measurements.
Namespace, policy and all three claim/PV pairs are rechecked against live API
UIDs/revisions, including claim-to-volume and Pod-owned scratch relationships.

The source is a trusted operator publisher outside the sandbox. Receipts are not
self-attestations and cannot be derived solely from Kubernetes desired state or
agentd reports. The reader accepts only root/AR-owned private files below a private
AR-only directory, at most 32 KiB, with a positive validity interval of at most
30 seconds. Missing, future, stale or mismatched evidence fails closed. Success is
not cached. The publisher must observe changes and stop publication on uncertainty;
it may not refresh the timestamp on a historical report.

This implements the consumer and verifier, not an automated host measurement
collector. Deploying a trusted publisher and collecting fresh end-to-end homelab
evidence remain explicit acceptance work. No historical Phase 3 fixture is promoted
into secure readiness. This distinction preserves ADR-0013 and ADR-0014 while
avoiding a new paid service, privileged workload or Kubernetes operator.

## Consequences

HTTP-only startup remains available without cluster/transport configuration.
The transport host controls existing admitted materializations; Phase 6 still owns
the user-facing creation workflow. Operators supply dedicated certificates,
read-only expected blueprints/policies and the independent evidence source.
The [runbook](../operations/agentd-hosting.md) describes these prerequisites.

AGD-020 remains open for trusted evidence publication/live acceptance, authenticated
final reporting and replacement-sandbox recovery. This decision does not introduce
cross-component APIs or move authority into a harness, receipt or command plan.
