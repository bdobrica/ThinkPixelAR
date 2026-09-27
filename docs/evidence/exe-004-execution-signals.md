# EXE-004 — durable Execution signal acceptance

Implemented 2026-09-27.

`POST /v1/executions/{execution_id}/signals` accepts the published signal envelope
(`user_input`, `permission_response`, `interrupt`) through an explicitly composed
`NewLocalSignaler`. Trusted authentication, current mutation/disclosure access,
and negotiated payload/capability policy are mandatory. Unknown/duplicate fields,
invalid JSON, more than 32 payload properties, and bodies exceeding 64 KiB fail
before persistence. Unsupported negotiated types return 422. Permission responses
must match outstanding requests and cannot expand existing authority.

Acceptance requires an ACTIVE Session at the bound generation, a RUNNING Execution,
and an intact, ACTIVE, unexpired local grant. Session, Execution and grant locks
serialize the decision against lifecycle changes and revocation. Non-local authority
is unsupported in this composition. Acceptance does not transition lifecycle state.

Migration 25 adds immutable, tenant-isolated Confidential signal bodies. One
transaction records the private body, ordered `signal.accepted` metadata event,
`execution.signal.v1` outbox intent, and replay-safe 202 PENDING operation. Bodies
never enter event/outbox metadata or replay responses. The operation ID is the
stable delivery identity; generic idempotency expiry retains signal keys.
Concurrent equivalent requests converge on the original acceptance. Conflicting
bodies return 409. Replays check current access but do not inject work, revalidate
old grant expiry, or run capability policy again.

Verified:

- Focused race tests for application, HTTP, PostgreSQL and migration packages.
- Every non-RUNNING state and a stale Session generation reject admission.
- Real TCP HTTP/PostgreSQL integration on isolated database `thinkpixelar_exe004`,
  with all 25 migrations applied: concurrent acceptance, normalized/conflicting
  replay, unsupported/invalid payload policy, tenant isolation, authorization
  revocation, expiry, outbox-failure rollback, exact body/event/outbox/replay counts,
  and replay after grant cancellation and terminalization/new Session generation.
- `go vet` on affected packages; pinned Redocly OpenAPI lint and bundle generation.

The integration fixture explicitly seeds READY and RUNNING lifecycle states and
supplies authentication and a bounded user-input policy. Executable identity/policy
composition remains API-001/API-002. The 202 operation proves durable acceptance,
not delivery: the outbox consumer/agentd bridge and delivery outcome reconciliation
remain separate work. That consumer must validate the persisted body digest,
current authority and Session/Execution/Attempt fences, recheck permission requests,
and deduplicate by operation ID. It must not retry ambiguous delivery when the
adapter cannot deduplicate, or retarget stale input to replacement compute.
Interrupt acceptance is not cancellation or evidence of a stopped process.
No live harness, AG signal path or end-to-end delivery was exercised here.
