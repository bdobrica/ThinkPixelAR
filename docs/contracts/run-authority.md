# RunAuthority and standalone LocalAuthority contract

Status: Normative Phase 0 contract.

## Purpose and boundary

`RunAuthority` is the AR port that decides whether one bounded Execution may be admitted and whether its authority remains valid. It normalizes standalone operator policy and integrated ThinkPixelAG governance without exposing AG transport types to the domain.

The port does not execute work, select physical Sandboxes, store provider credentials, authorize enterprise tool side effects, or replace Session/Attempt fencing. A valid grant is necessary but never sufficient for an Attempt-originated mutation.

## Domain contract

The following Go-like definitions are normative semantics, not a frozen source-level API:

```go
type RunAuthority interface {
    Admit(ctx context.Context, request ExecutionRequest) (ExecutionGrant, error)
    Validate(ctx context.Context, grant ExecutionGrant, at time.Time) (AuthorityStatus, error)
    Heartbeat(ctx context.Context, grant ExecutionGrant, attempt AttemptRef) (AuthorityStatus, error)
    Complete(ctx context.Context, grant ExecutionGrant, result ResultRef) error
    Fail(ctx context.Context, grant ExecutionGrant, failure FailureRef) error
    Cancel(ctx context.Context, grant ExecutionGrant, reason CancelReason) error
}
```

All methods receive authenticated caller/worker context out of band. Tenant, principal, delegation, or worker identity supplied only by untrusted JSON MUST NOT be treated as authoritative.

## Requests, grants, and status

### ExecutionRequest

An admission request contains bounded, validated values:

- authenticated tenant and principal context/reference;
- Session identity and current immutable agent/runtime binding;
- mutation idempotency identity and canonical request digest;
- requested operation class and protected input/reference (not logged by the authority port);
- requested Runtime Profile and optional resource, duration, network, and capability constraints;
- current Session state/version and proposed execution generation;
- external Run/authority reference when integrated;
- request/trace correlation references.

The request MUST NOT carry raw provider/downstream credentials. Free-form content is not required for local policy and MUST NOT be copied into grants or authority telemetry.

### ExecutionGrant

An admitted grant is immutable and contains:

- globally unique `grant_id` and stable issuer/mode (`local` or `thinkpixelag`);
- tenant, principal/delegation reference, Session, request digest, and proposed generation binding;
- immutable resolved agent image digest, adapter kind/version, and agent/version evidence;
- allowed Runtime Profile plus resolved resource, storage, network, duration/deadline, and capability ceilings;
- `issued_at`, `not_before` where used, and mandatory `expires_at`/deadline from the authority's trusted clock;
- external Run, lease, and fencing references where supplied by an integrated authority;
- policy/config revision digest and safe decision reason code;
- initial active status reference.

The persisted Execution stores the immutable grant snapshot or integrity-bound reference in the same admission transaction. Mutable status such as cancelled, expired, revoked, completed, or failed is stored separately and cannot widen the snapshot.

### AuthorityStatus

Status is one of `ACTIVE`, `CANCELLED`, `EXPIRED`, `REVOKED`, `COMPLETED`, or `FAILED`, plus safe reason and observed validity time. Only `ACTIVE` permits forward work. An unknown/unavailable status fails closed for new work; recovery MAY wait within the Execution deadline but MUST NOT assume authority remains active.

## Method semantics

### `Admit`

- Validates authenticated context, request bounds, Session/version consistency, requested profile/capabilities, idempotency ownership/digest, and authority-specific policy.
- Returns a new immutable grant or the exact prior grant for a replay with the same authority scope, idempotency identity, and digest.
- Rejects a reused idempotency identity with a different digest.
- Does not partially admit: grant creation/status and any authority-owned durable idempotency result are atomic.
- Does not create the AR Execution itself. AR atomically binds the returned grant during Session Execution admission; an unused grant is harmless and expires/can be cancelled.
- Returns typed denial/unavailable/conflict errors without leaking cross-tenant existence or sensitive policy inputs.

### `Validate`

- Verifies grant integrity, issuer/mode, tenant/Session binding, time window, mutable status, policy-invalidating revocation where supported, and external lease/fence where applicable.
- Uses the authority implementation's trusted UTC clock; caller-provided `at` is an injectable/testing representation, never a sandbox timestamp.
- Is safe to repeat and has no permission-widening side effect.
- Returns `ACTIVE` only when forward work is currently authorized.

AR calls `Validate` before materialization/start/resume/replacement and before accepting security-relevant state mutations. Validation does not replace local Session generation/current-Attempt predicates.

### `Heartbeat`

- Reports bounded liveness for the current AR Attempt and renews an external lease only if that authority supports and permits renewal.
- Validates the AR Attempt reference/fence supplied from trusted persisted context.
- Never extends the immutable Execution deadline or widens resources/capabilities.
- Is idempotent for the same grant, Attempt, fence, and heartbeat interval identity.

LocalAuthority treats Heartbeat as `Validate`; it does not create a distributed lease or extend expiry.

### `Complete` and `Fail`

- Report exactly one AR terminal outcome using stable grant, Execution-result event, and authority fence identity.
- Accept bounded result/evidence references and safe reason codes, not arbitrary logs, prompts, model output, or credentials.
- Are idempotent for the same terminal identity and digest.
- Reject/log as a conflict a different outcome after a terminal winner; they never rewrite the winner.
- In integrated mode, stale lease/fence outcomes fail and require reconciliation with authoritative AG state.

AR persists local terminal state and outbox intent before invoking these methods. Retry after transport uncertainty is safe under the stable terminal identity.

### `Cancel`

- Marks active authority cancelled/revoked according to issuer semantics and prevents future `ACTIVE` validation.
- Is idempotent when already cancelled or terminal.
- Does not by itself prove the harness/Sandbox stopped; AR's cancellation state machine and Attempt fencing enforce that separately.
- Rejects a caller that lacks authority to cancel the bound tenant/operation.

## Error and availability model

The port returns typed classes: `DENIED`, `INVALID_GRANT`, `CONFLICT`, `EXPIRED`, `CANCELLED`, `REVOKED`, `STALE_FENCE`, `UNAVAILABLE`, and `INTERNAL`. Error messages and telemetry contain safe reason codes, not credentials, request content, or raw upstream responses.

- Denial and invalidity fail the requested operation without retry unless inputs/authority change.
- `UNAVAILABLE` may be retried with bounded backoff while the immutable deadline permits, but no new sandbox work begins and active work follows the configured fail-closed interruption/reconciliation policy.
- Ambiguous completion reporting is reconciled by stable terminal identity; AR does not execute the operation again to obtain a cleaner result.

## LocalAuthority

`LocalAuthority` provides bounded standalone runtime admission from trusted operator configuration. It is deliberately not an emulation of ThinkPixelAG governance.

### Required configuration

LocalAuthority MUST be disabled unless the deployment explicitly selects `authority.mode: local`. Production-integrated configuration MUST NOT silently fall back to local mode when AG is unavailable or misconfigured.

The validated configuration defines:

- allowed Runtime Profile names and optional per-profile overrides;
- maximum Execution duration and a non-zero default duration;
- CPU, memory, ephemeral storage, durable storage, GPU/device, and architecture ceilings;
- allowed network-profile names, with the configured safe default;
- allowed agent/runtime bindings or an operator-approved resolver boundary;
- allowed capability classes (for example shell/process execution or adapter fork capability);
- per-tenant/global concurrent Execution and admission-rate bounds where available at this phase;
- immutable configuration revision/digest exposed in grant evidence;
- optional tenant/principal allowlists derived from authenticated identity.

Startup fails on missing duration bounds, unknown profiles/capabilities, contradictory minimum/maximum values, non-positive resources, unbounded fields, or a default that is not in its allowlist. Secret values are neither needed nor accepted in policy configuration.

### Admission algorithm

For an authenticated request, LocalAuthority performs these steps in order:

1. Verify local mode is explicitly active and authenticated tenant/principal is permitted.
2. Validate Session state/version, immutable agent/runtime evidence, proposed generation, idempotency key, and request digest shape.
3. Resolve the requested Runtime Profile from operator configuration. A missing request uses the configured default; an unknown/disallowed profile is denied, never substituted with a more permissive profile.
4. Intersect requested duration/resources/storage/network/capabilities with profile and global ceilings. Omitted values receive bounded defaults. Any explicit value above a ceiling is denied rather than silently truncated, so the caller sees that its requested contract was not admitted.
5. Set `issued_at` from the injected UTC clock and `expires_at = min(issued_at + admitted_duration, any authenticated caller/session deadline)`. Duration is always finite and positive.
6. Generate an immutable local grant with mode `local`, issuer `thinkpixelar/local`, no AG Run/lease/fence claim, and the configuration revision digest.
7. Persist/replay the authority idempotency result where the adapter owns such persistence; return it for atomic AR Execution binding.

LocalAuthority cannot authorize enterprise downstream side effects. Tool operations still require their configured trusted gateway/authorization, and direct egress is controlled separately by the admitted network profile.

### Local grant lifecycle

- A local grant begins `ACTIVE` and becomes `EXPIRED` when the trusted clock reaches `expires_at`.
- `Cancel` atomically changes an active grant to `CANCELLED`; it cannot return to active.
- `Complete` and `Fail` atomically store the first terminal status/result digest (`COMPLETED` or `FAILED`).
- Status transitions use compare-and-swap and one terminal winner. Completion/failure after cancellation/expiry is rejected unless the call is an idempotent replay of an already committed terminal winner.
- Local grants are never renewed. A new user operation requires a new grant/Execution. Infrastructure replacement remains under the same grant only while it is active and before its deadline.
- Process restart reconstructs status from durable authority/Execution state; it does not mint replacement authority silently.

### LocalAuthority validation

`Validate` returns `ACTIVE` only when:

- stored immutable grant digest and supplied/reference grant match;
- mode/issuer are local and local mode is still explicitly configured for this deployment;
- tenant, Session, request digest, generation, agent/runtime, profile, and configuration revision evidence match the bound Execution;
- current time is within the grant window;
- mutable status is active; and
- requested action remains inside the immutable grant.

A later operator configuration change does not widen an issued grant. A security/operator policy MAY invalidate grants issued under a blocked revision, in which case validation returns `REVOKED`; it cannot reinterpret them more permissively.

### Explicit non-guarantees

Standalone LocalAuthority does not provide:

- ThinkPixelAG governed Run identity or agent/version approval workflow;
- distributed worker leases or AG fencing tokens;
- enterprise budgets, delegation, revocation distribution, or governance evidence;
- authorization for GitHub, Jira, Slack, deployment, database, or other enterprise side effects;
- provider credentials, model routing, or guardrail decisions;
- equivalence certification with integrated mode.

## Visibility and audit

Every admitted Execution exposes a safe immutable `authority_mode` (`local` or `thinkpixelag`) and issuer reference in API status, Runtime Events, structured logs/traces, and evidence. Metrics use bounded `authority_mode` only. Local mode emits an operator-visible startup log/condition and MUST be visibly labeled in deployment diagnostics; it is not described as “governed” in user-facing output.

Authority records include grant ID, mode/issuer, policy revision, safe decision code, issued/expiry times, bound AR identities, and terminal status/evidence references under tenant authorization. They exclude prompts, output, credentials, and arbitrary upstream payloads according to the data-classification contract.

## Concurrency and recovery

- Authority status transitions are durable, transactional, optimistic-versioned, and idempotent.
- AR's transactional outbox decouples local state commit from authority reporting.
- After restart, AR validates authority before starting/resuming/replacing work and reconciles any pending terminal report.
- A grant cannot be transferred to another Session, Execution, generation, tenant, agent version, or Runtime Profile.
- A valid grant plus invalid current-Attempt/Session fence is rejected; a valid local fence plus invalid grant is also rejected.

## Verification requirements

### RunAuthority conformance

Every implementation MUST pass a common conformance suite covering admission replay/conflict, immutable grants, wrong tenant/Session/generation, expiry boundary, cancellation, one terminal winner, duplicate/conflicting results, typed/redacted errors, unavailable behavior, and restart/replay.

### LocalAuthority

Tests MUST cover:

- explicit mode selection and no fallback from integrated mode;
- defaults within bounds and denial of every over-ceiling/unknown value;
- authenticated identity binding and rejection of JSON-only caller identity;
- exact finite deadline calculation with an injected clock;
- immutable agent/runtime, profile, resource, network, capability, and policy-revision evidence;
- Heartbeat without renewal;
- cancellation/expiry/completion/failure races and terminal immutability;
- replacement Attempt before expiry and denial at/after expiry;
- configuration revision invalidation without permission widening;
- API/event/log/trace visibility of `authority_mode=local` and absence from high-cardinality metric labels;
- absence of provider/downstream credentials and content from grants, errors, telemetry, and persisted authority evidence.

