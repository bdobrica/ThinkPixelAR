# SandboxProvider contract

Status: Normative Phase 0 contract.

## Purpose

`SandboxProvider` materializes replaceable isolated compute from an AR-neutral desired specification. It hides Kubernetes Agent Sandbox and any future backend from domain, application, API, event, and persistence contracts.

The provider does not own Session/Execution/Attempt lifecycle, authority, Workspace semantics, harness behavior, checkpoint publication, credentials, or recovery decisions. It reports infrastructure observations; AR decides their domain meaning.

## Interface

The following Go-like contract defines semantics, not final package syntax:

```go
type SandboxProvider interface {
    Capabilities(ctx context.Context) (SandboxProviderCapabilities, error)
    Acquire(ctx context.Context, request AcquireSandboxRequest) (SandboxHandle, error)
    Get(ctx context.Context, sandboxID SandboxID) (SandboxStatus, error)
    Suspend(ctx context.Context, sandboxID SandboxID, operation OperationIdentity) error
    Resume(ctx context.Context, sandboxID SandboxID, operation OperationIdentity) (SandboxHandle, error)
    Release(ctx context.Context, sandboxID SandboxID, operation OperationIdentity) error
}
```

All inputs and outputs are immutable value objects. Implementations must be safe for concurrent calls from multiple AR replicas and repeated calls after ambiguous responses/restarts.

## Neutral types

### Provider identity and capabilities

```go
type SandboxProviderCapabilities struct {
    ProviderKind       string
    ContractVersion    string
    SupportsSuspend    bool
    SupportsResume     bool
    SupportsWarmPool   bool
    IsolationClasses   []IsolationClass
    Architectures      []Architecture
    VolumeAttachment   []VolumeAttachmentMode
    NetworkClasses     []NetworkClass
}
```

Capabilities are a bounded, stable vocabulary and are not permission. AR validates requested Runtime Profile requirements at startup/admission and stores the provider capability/version evidence used for resolution. A changed capability set cannot silently weaken a materialized Execution.

### Acquire request

```go
type AcquireSandboxRequest struct {
    Operation          OperationIdentity
    TenantID           TenantID
    SessionID          SessionID
    ExecutionID        ExecutionID
    AttemptID          AttemptID
    ExecutionGeneration uint64
    AttemptOrdinal     uint64
    Runtime            ResolvedAgentRuntime
    Profile            ResolvedRuntimeProfile
    Workspace          WorkspaceAttachmentSpec
    AgentdBootstrap    AgentdBootstrapReference
    Labels             map[string]string
}

type OperationIdentity struct {
    OperationID        string
    RequestDigest      string
}
```

`OperationID` is stable for all retries of one logical external operation; `RequestDigest` is over the normalized complete request. Reusing an operation ID with a different digest is a conflict. Tenant/AR identities come from trusted persisted state, not a sandbox or caller-provided free-form map.

`ResolvedAgentRuntime` contains the validated digest-pinned image, platform, entrypoint, adapter/`agentd` requirements, and persistent path declarations from the immutable Session binding. `ResolvedRuntimeProfile` contains a complete immutable per-Execution snapshot of abstract constraints and provider-specific opaque configuration only inside the adapter. Public/domain objects do not carry raw Kubernetes templates.

`WorkspaceAttachmentSpec` is a provider-neutral reference and mount intent created by the WorkspaceProvider. The sandbox provider may wire the attachment into compute but cannot create, clone, publish, or delete Workspace generations. `AgentdBootstrapReference` is an opaque, short-lived sandbox-scoped bootstrap reference; it contains no long-lived provider/downstream credential.

Labels use a closed allowlist of bounded low-cardinality keys and sanitized values. They are diagnostics/ownership hints, never authorization.

### Handle

```go
type SandboxHandle struct {
    SandboxID          SandboxID
    ProviderKind       string
    ProviderReference string
    State              SandboxState
    IsolationClass     IsolationClass
    Architecture       Architecture
    CreatedAt          time.Time
    ObservedAt         time.Time
    Endpoint           SandboxEndpointReference
}
```

`SandboxID` is an AR-generated opaque stable identity. `ProviderReference` is opaque outside the adapter and bounded for storage/diagnostics. It is not exposed as a public Kubernetes identity or trusted when echoed by the sandbox. `Endpoint` is a protected reference used by the separately defined authenticated transport; it is not a bearer credential or unrestricted URL.

### Status and states

```go
type SandboxStatus struct {
    Handle             SandboxHandle
    State              SandboxState
    Reason             SandboxReason
    ProviderGeneration string
    Conditions         []SandboxCondition
    Effective          EffectiveSandboxFacts
}
```

Neutral states are:

| State | Meaning |
| --- | --- |
| `REQUESTED` | AR binding exists; provider acceptance may still be reconciled. |
| `PROVISIONING` | Provider is creating/preparing compute. |
| `READY` | Required infrastructure and authenticated transport endpoint can be established; harness readiness is separate. |
| `ACTIVE` | Provider observes running compute. This does not assert AR Execution authority. |
| `SUSPENDING` | Provider accepted a suspend operation. |
| `SUSPENDED` | Provider reports compute suspended according to its capability contract. |
| `RESUMING` | Provider is restoring suspended compute. |
| `RELEASING` | Release accepted; physical cleanup may remain. |
| `RELEASED` | Provider confirms owned compute is absent/terminal. |
| `FAILED` | Provider reports a terminal materialization failure for this physical sandbox. |
| `UNKNOWN` | Current provider truth cannot be established. It never means absent or safe. |

Provider-native phases map deterministically to one neutral state and bounded reason/condition codes. Raw provider messages are not durable domain errors and are redacted/truncated before diagnostics.

`EffectiveSandboxFacts` reports externally observed isolation class, image digest, architecture, resource ceilings, network class, service-account exposure, privileged/host namespace/host path/runtime socket/device facts, and attachment references where the provider can verify them. Secure readiness requires effective facts to satisfy the resolution snapshot; desired configuration alone is insufficient.

## Operation semantics

### `Capabilities`

- Returns the implementation's current contract/capability vocabulary without tenant data.
- Is bounded, cacheable for a short operator-configured interval, and revalidated on errors/version changes.
- Unknown required capabilities fail startup/profile validation or admission; AR does not infer support from provider names.

### `Acquire`

- Requires a complete normalized request and stable operation identity.
- Creates or returns exactly one provider resource for the AR `SandboxID`/Attempt binding.
- Same operation ID and digest returns the same Sandbox identity/handle even after timeouts or controller restart.
- Same operation ID with a different digest returns `CONFLICT` and creates nothing.
- Persists/reserves the AR SandboxBinding before the external call; the adapter applies ownership metadata sufficient for safe lookup/cleanup but metadata is not authorization.
- Returns as soon as provider acceptance/known handle is durable; it need not block until `READY`. Readiness is reconciled with `Get`.
- Never selects a weaker profile, mutable image tag, alternate Workspace, or different tenant when capacity is unavailable.

Provider creation may be asynchronous. `Acquire` returning an error does not prove no resource was created; retry/reconcile by operation identity before creating a new Attempt.

### `Get`

- Reads by AR SandboxID through the persisted binding and provider ownership metadata.
- Returns the neutral status plus observed effective facts and observation time.
- `NOT_FOUND` means the provider can authoritatively prove the bound resource absent. `UNKNOWN`/`UNAVAILABLE` means absence cannot be proven.
- Does not mutate Session/Execution/Attempt state or create/release resources.
- Repeated reads are safe and bounded by API timeout/response-size limits.

### `Suspend`

- Requires advertised suspend capability, current ownership/binding, and a new stable operation identity.
- Is called only after AR's Session suspend guards/checkpoint boundary have succeeded; provider does not validate checkpoint correctness.
- Same operation replay is idempotent. Suspending an already suspended sandbox succeeds. A released/missing sandbox returns a stable not-found/already-released result for AR reconciliation.
- Acceptance transitions observed state toward `SUSPENDING`; AR polls `Get` for `SUSPENDED` before treating physical suspend as confirmed.
- Suspension never preserves or renews Execution authority.

### `Resume`

- Requires advertised resume capability and a previously suspended, still-owned Sandbox.
- Same operation/digest returns the same resumed Sandbox identity; a provider that creates replacement identity during resume must expose that as a new Acquire/replacement capability rather than silently changing the handle.
- Returns acceptance/handle; AR proves effective readiness and starts a fresh harness process with fresh Execution authority separately.
- If provider-native resume cannot meet the original resolution snapshot, it fails; AR may choose release plus replacement Acquire under its recovery contract.

### `Release`

- Is idempotent and convergent. Releasing absent/already released owned resources succeeds.
- Removes only the exact bound provider resource and provider-owned ephemeral attachments/credentials; it does not delete Session Workspace, checkpoint, artifacts, or authoritative AR history.
- Marks release acceptance but AR retains cleanup work/binding until `Get` or provider evidence confirms absence.
- A binding mismatch, ambiguous ownership, or suspicious duplicate fails closed and enters orphan investigation; the adapter never broad-deletes by tenant/session labels.
- Finalizer/deletion delays are `RELEASING`, not success. Force deletion is an operator policy outside the generic port.

## Error model

Provider errors map to stable classes:

| Class | Meaning / AR response |
| --- | --- |
| `INVALID_REQUEST` | Contract/profile/runtime request invalid; do not retry unchanged. |
| `UNSUPPORTED` | Required capability/version unavailable; fail admission/materialization. |
| `CONFLICT` | Operation identity/digest or ownership/state conflict; reconcile, never overwrite. |
| `NOT_FOUND` | Exact binding authoritatively absent; recovery policy may replace. |
| `CAPACITY` | No suitable capacity currently; bounded retry/backoff within deadline. |
| `UNAVAILABLE` | Provider truth unavailable/partitioned; do not infer absence or create duplicate blindly. |
| `TIMEOUT` | Outcome ambiguous; retry/reconcile same operation identity. |
| `PERMISSION` | AR provider credential/configuration denied; fail closed/operator action. |
| `INTEGRITY` | Effective image/profile/attachment/ownership differs; quarantine/release safely and fail. |
| `INTERNAL` | Sanitized unexpected adapter failure; bounded retry only when classified safe. |

Errors include retryability and safe reason code, not raw Kubernetes objects, manifest content, tokens, provider payloads, or tenant content.

## Persistence and reconciliation

AR persists `SandboxBinding` independently of the provider resource:

- AR/tenant/Session/Execution/Attempt/generation identities;
- provider kind, SandboxID, opaque provider reference;
- acquire/suspend/resume/release operation identities and request digests;
- desired resolution snapshot digest;
- last neutral state/reason/effective-fact digest/observation time;
- cleanup/recovery ownership and optimistic version.

After restart, reconciliation loads desired AR state and binding, then calls `Get` and repeats only the same idempotent operation. A missing Sandbox may create a replacement Attempt only after the old Attempt is durably fenced. Orphan scans use exact persisted references plus ownership proof and never infer ownership from a human-readable name alone.

## Kubernetes Agent Sandbox adapter boundary

The production adapter may use Kubernetes clients, `Sandbox`, `SandboxTemplate`, `SandboxClaim`, warm pools, RuntimeClass, Pods, PVCs, Conditions, resource quantities, Namespaces, and API errors internally. None of these types, Group/Version/Kind strings, YAML fragments, label keys, or watch resource versions appear in:

- domain/application method signatures;
- public REST/SSE schemas;
- persisted Runtime Profile domain snapshots (except an opaque adapter configuration digest/reference);
- normalized Runtime Events;
- Workspace, AgentRuntimeSpec, ExecutionGrant, or harness contracts.

Adapter tests prove deterministic translation both directions. A second production provider can implement this contract without emulating Kubernetes objects.

## Security invariants

- Provider credentials exist only in trusted AR control-plane adapters and never enter the Sandbox.
- Sandbox-supplied identity/status is not provider status.
- Desired spec cannot request host privilege, service-account tokens, credential mounts, or weaker isolation outside the operator-approved Runtime Profile.
- Effective state is verified before secure readiness.
- Tenant/object ownership is enforced through trusted binding plus provider API scope; labels and opaque IDs alone do not authorize deletion/read.
- A warm/persisted provider resource cannot be attached until residue/identity checks pass.
- Network, storage, resource, and isolation enforcement are external to compromised sandbox processes.

## Conformance requirements

Every provider implementation MUST pass a common suite covering:

- capability/version negotiation and unsupported requirements;
- same-key Acquire replay, conflicting digest, timeout-after-create, and concurrent callers;
- neutral state/reason/effective-fact translation;
- readiness with desired-versus-effective mismatch;
- Get absent versus unavailable distinction;
- suspend/resume supported, unsupported, replay, and intermediate states;
- Release replay, delayed deletion, missing resource, and ownership mismatch;
- restart reconstruction from persisted binding;
- orphan/duplicate resources without broad deletion;
- bounded/redacted errors and no provider-type leakage at package/API/schema boundaries;
- malicious labels, references, status text, and provider objects;
- stale Attempt/generation fencing before any provider result mutates AR state.

