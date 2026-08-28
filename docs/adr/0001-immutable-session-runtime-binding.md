# ADR-0001: Bind each Session to one immutable agent runtime

- Status: Accepted
- Date: 2026-08-28
- Deciders: ThinkPixelAR maintainers
- Consulted: ThinkPixelAR architecture and ThinkPixelAG version contracts
- Supersedes: None
- Superseded by: None

## Context

A durable Session may preserve Workspace and vendor conversation state for days or months while every Sandbox and harness process is replaced. Resuming that state with a different image, harness adapter, entrypoint, durable-path contract, or platform ABI can corrupt state, change behavior, bypass approval evidence, or silently execute a different agent than the user/governance system authorized.

ThinkPixelAG resolves immutable approved agent versions per governed Run. Standalone AR resolves operator-approved runtime manifests. Versions can later be deprecated or revoked. Continuation therefore needs both stable runtime identity and fresh authorization; persistence alone cannot authorize execution.

## Decision

### Binding point and identity

Every Session MUST be bound to exactly one immutable `AgentRuntimeSpec` before AR begins initial Workspace source materialization, Sandbox acquisition, vendor-state creation, or harness startup.

Session creation resolves the requested agent reference to a canonical runtime specification and persists its binding atomically with the Session in `PROVISIONING`. If resolution, approval, digest verification, or persistence fails, no Session is created and no external materialization begins.

The immutable binding contains at minimum:

- authority namespace/mode and stable agent/version identifiers;
- canonical OCI image reference pinned by digest and the manifest/spec digest;
- adapter kind plus adapter protocol/compatibility requirement;
- entrypoint and durable vendor-state path contract;
- canonical Workspace mount and required Runtime Profile/platform requirements;
- source authority decision/evidence and resolution timestamp;
- schema version and an integrity digest over the normalized runtime specification.

Human-friendly agent names, tags, channels, “latest”, mutable image tags, registry redirects, or current approval pointers are resolution inputs only. They are never the binding identity.

Once persisted, the Session binding MUST NOT be updated in place, including by an operator configuration change, image tag movement, newer approval, adapter upgrade, suspend/resume, recovery, restore, or retry. Historical Executions also store the exact binding/spec digest they used.

### Execution and continuation authorization

Every new Execution requires fresh RunAuthority admission. AR compares the resulting ExecutionGrant and resolved evidence to the Session binding before advancing the Session execution generation.

Continuation is allowed only when all are true:

1. authenticated caller is authorized for the Session and requested operation;
2. authority grants the exact bound agent/version and runtime specification digest;
3. the version remains eligible for continuation under current policy and revocation state;
4. Runtime Profile resolution satisfies the bound minimum requirements without changing runtime identity;
5. adapter implementation is compatible with the bound adapter contract and checkpoint/vendor-state versions;
6. checkpoint and Workspace generation integrity is valid where resuming;
7. deadline, resource, network, lease, fencing, and Session lifecycle guards pass.

A fresh authorization MAY narrow resources, duration, network access, or capabilities relative to a prior Execution. It MUST NOT replace the bound image, adapter kind/protocol contract, entrypoint, persistent paths, platform ABI, or agent/version identity.

In integrated mode, AG must return immutable version-resolution evidence sufficient for equality with the binding. A caller-supplied digest does not prove approval. In standalone mode, LocalAuthority resolves only operator-approved digest-pinned manifests and records the policy/config revision.

### Deprecation and revocation

Deprecation and revocation are different:

- **Deprecated** means not normally selected for new Sessions. Existing bound Sessions MAY continue only if the current authority explicitly permits continuation of that exact deprecated digest. No automatic upgrade occurs.
- **Revoked** means the version is ineligible for new Session creation, new Execution admission, resume, recovery replacement, harness restart for forward work, or fork materialization.

When revocation is detected before work, AR fails closed without materializing compute. When detected during an active Execution, AR stops accepting input, fences/interrupts the current Attempt, and reconciles with the authoritative Run outcome. An untrusted Sandbox cannot continue merely because it has old files or credentials. Gateways independently enforce revocation/expiry of Execution-scoped authority.

If the authority/revocation service is unavailable or freshness cannot be proven, AR does not start, resume, or replace work. A currently running Attempt may continue only under an explicitly documented bounded disconnected-operation policy from the authority; the initial secure integrated profile has no such widening and stops before its lease/freshness deadline.

Revocation does not delete or rewrite Session, Workspace, checkpoint, or historical evidence. Read/export/delete operations remain subject to policy. A revoked Session is diagnosable and may be closed, but cannot execute until an explicit future migration mechanism authorizes a new Session.

### Future migration semantics

Automatic in-place Session version migration is excluded from the release candidate.

A future migration MUST be an explicit governed operation that:

1. leaves the source Session and binding immutable;
2. creates a new Session identity with a newly authorized immutable runtime binding;
3. selects an explicit source checkpoint/Workspace generation;
4. clones or transforms state into an independently writable Workspace/vendor-state generation;
5. invokes adapter-specific compatibility/migration code only through a declared capability and versioned format contract;
6. records source and destination spec digests, transformation artifact/digest, actor, policy decision, and validation evidence;
7. validates the destination before allowing execution and supports safe abandonment without damaging the source.

If no compatible migration exists, the user may start a clean Session or retain/close the old one. “Change version” is never implemented as updating `session.agent_version_id`.

Session fork without version migration retains the exact source binding. A fork that changes version is the explicit migration operation above, not an ordinary fork.

## Alternatives considered

### Resolve the current approved version on every resume

Rejected. It silently changes executable code and compatibility underneath durable vendor/Workspace state, undermines reproducibility, and can bypass the approval evidence attached to earlier work.

### Allow mutable tags while persisting the tag string

Rejected. Registry tag movement makes identity and rollback unverifiable and permits supply-chain substitution.

### Mutate the Session binding during upgrade

Rejected for the RC and future default. It destroys historical identity, complicates rollback, and makes old checkpoints ambiguous. A new Session with lineage is safer and auditable.

### Permanently allow an approved-at-creation version despite later revocation

Rejected. Durable state does not confer durable authority; known-vulnerable or prohibited code must fail closed for forward work.

### Always forbid deprecated versions

Rejected as a universal rule. Deprecation commonly prefers a newer version without declaring the old one unsafe. Current authority may explicitly permit bound-Session continuation while refusing new selection.

## Consequences

### Positive

- Resume and recovery are reproducible and attributable to one immutable runtime.
- Governance approval and supply-chain evidence can be compared exactly on every Execution.
- Vendor/checkpoint compatibility has a stable anchor.
- Revocation cannot be bypassed by persistent state or mutable tags.
- Migration history is explicit and rollback leaves the source intact.

### Negative and trade-offs

- Existing Sessions do not automatically receive fixes or new features.
- Operators must retain or reproduce approved image digests and compatible adapters while Sessions remain recoverable.
- Revocation can strand continuity until a migration path exists.
- Explicit migration requires storage cloning/transformation and adapter-specific validation work.

## Security

- AR verifies OCI digest syntax and pull/result identity; signature/provenance policy remains an independent verification layer.
- Runtime spec equality uses canonical schema/digest comparison, not display names or partial fields.
- Revocation and approval are obtained from trusted authority with freshness evidence; sandbox claims are ignored.
- Checkpoints contain binding/spec references but no execution credentials.
- Error and audit records use opaque references and safe decision codes under the data-classification contract.

## Operations

- Garbage collection MUST retain images, adapters, manifests, and checkpoint formats needed by active/retained Sessions or mark them explicitly unrecoverable before deletion.
- Diagnostics expose bound digest, adapter contract, authority mode, eligibility status, and safe incompatibility reason.
- Registry outage may delay materialization but cannot cause tag fallback.
- Backup/restore preserves immutable bindings and lineage.
- Operators need inventory for Sessions bound to deprecated/revoked or soon-unsupported runtimes.

## Compatibility

Runtime compatibility is a conjunction of `AgentRuntimeSpec` schema, image platform, adapter kind/protocol range, `agentd` protocol, vendor-state/checkpoint format, and required Runtime Profile capabilities. A compatible adapter implementation may be patched/upgraded if it truthfully supports the bound contract and passes conformance; changing the adapter kind or bound protocol requirement requires migration.

Unknown schema versions, unavailable digests, incompatible platforms, unsupported adapter ranges, or checkpoint mismatches fail before forward work. Downgrade is not automatic.

## References

- [Normative domain glossary](../contracts/glossary.md)
- [RunAuthority and standalone LocalAuthority](../contracts/run-authority.md)
- [ThinkPixelAGAuthority integration](../contracts/thinkpixelag-authority-integration.md)
- `../ThinkPixelAG/docs/contracts/domain-model.md` at review revision recorded by ARC-010

