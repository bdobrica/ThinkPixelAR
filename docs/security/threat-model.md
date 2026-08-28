# Primary threat model

Status: Normative Phase 0 security contract.

## Scope and security objective

This threat model covers ThinkPixelAR's control plane, persistence, sandbox materialization, sandbox-local execution, Workspace and checkpoint handling, gateway connections, and recovery. It applies in standalone and ThinkPixel-integrated deployments.

The primary objective is to preserve tenant isolation, authority integrity, credential confidentiality, durable-state integrity, and attributable execution even when all software and content inside an Agent Sandbox is hostile.

## Foundational compromise assumption

Assume complete compromise and arbitrary collusion among:

- the vendor harness;
- generated code and shell commands;
- repository source, build scripts, tests, hooks, and configuration;
- downloaded or preinstalled dependencies;
- `thinkpixel-agentd` and every child process in its sandbox;
- prompts or tool output designed to manipulate the harness.

The attacker can read and modify sandbox memory and files, inspect environment variables and process arguments, forge local protocol messages, replay previously observed messages, consume allocated resources, race lifecycle operations, attempt arbitrary network connections, and persist malicious data into writable Workspace or vendor-state paths.

Consequently, no sandbox claim about identity, authorization, resource use, completion, checkpoint integrity, tool outcome, or policy compliance is authoritative without external validation.

## Protected assets

1. Tenant identity and isolation.
2. Session, Execution, Attempt, generation, and fencing integrity.
3. AG Run authority, leases, resource envelopes, and revocation state.
4. PostgreSQL state, transactional ordering, idempotency, and audit evidence.
5. Model-provider, SCM, cloud, Kubernetes, database, and enterprise credentials.
6. Prompts, model output, repository content, Workspace data, vendor state, checkpoints, artifacts, logs, events, and traces according to their classification.
7. Host, node, Kubernetes control plane, container runtime, other Sandboxes, and trusted gateway services.
8. OCI image, Runtime Profile, adapter, and checkpoint provenance/integrity.
9. Availability within explicitly granted capacity and rate bounds.

## Trust boundaries and principals

The authoritative boundaries are shown in [system context and trust boundaries](../architecture/system-context.md).

- The trusted plane contains AR, AG when configured, LLMGW, TG, GR, PostgreSQL, infrastructure controllers, trusted registry services, and trusted storage control interfaces.
- The untrusted plane begins at the sandbox boundary and includes `agentd`, the harness, repository/generated code, dependencies, and their writable state.
- External clients and providers are separately authenticated principals; network location alone grants no trust.
- Cluster and storage administrators are privileged operational actors. Their compromise is not prevented by sandbox isolation and requires infrastructure IAM, audit, encryption, and separation-of-duty controls.

## Entry points

- REST/SSE requests and caller credentials;
- AR-to-AG and gateway APIs;
- Kubernetes Agent Sandbox resources and watch events;
- the AR-to-`agentd` transport;
- harness structured protocol messages;
- OCI images and registry metadata;
- Workspace sources, snapshots, vendor state, checkpoints, and artifacts;
- model input/output and tool input/output;
- configuration, logs, metrics, traces, backups, and restore paths;
- DNS and network egress from the sandbox.

## Threats, required controls, and verification

| ID | Threat | Mandatory controls | Required verification |
| --- | --- | --- | --- |
| TM-01 | A sandbox obtains Kubernetes or host control. | No service-account token; no host namespaces, hostPath, privileged mode, runtime sockets, or unnecessary capabilities; strong Runtime Profile isolation; deny cloud metadata and Kubernetes API egress. | Inspect effective pod/sandbox spec; hostile fixture probes token paths, metadata, API, host mounts, devices, and runtime sockets in a Kata-capable environment. |
| TM-02 | One tenant or Session reads or mutates another's state. | Tenant scope on every repository query and object key; unguessable identifiers are not authorization; per-Session Workspace binding; authenticated sandbox identity; storage and network isolation. | Cross-tenant API, database, storage, transport, and identifier-enumeration tests. |
| TM-03 | A stale or replaced Attempt mutates current state or reports a terminal result. | Monotonic Session execution generation; current-Attempt designation; compare-and-swap/transactional updates; AG fencing where configured; terminal transitions are idempotent and generation-bound. | Delayed/replayed message tests after cancellation, retry, controller restart, and replacement Sandbox creation. |
| TM-04 | Sandbox code steals long-lived model-provider or downstream credentials. | Provider/downstream credentials remain in LLMGW/TG; sandbox receives only short-lived, audience- and Execution-bound authority; secrets excluded from persistent paths and telemetry. | Environment/filesystem/process inspection; expired/revoked-token denial; checkpoint and artifact secret scanning. |
| TM-05 | Compromised `agentd` forges identity, state, usage, or completion. | Sandbox-scoped mutual authentication; AR maps transport identity to SandboxBinding; AR validates lifecycle, Attempt, generation, authority, and deadlines; authoritative accounting comes from trusted infrastructure/gateways. | Cross-sandbox impersonation, malformed state, impossible transition, false usage, replay, and obsolete-Attempt tests. |
| TM-06 | Prompt injection or local approval triggers an unauthorized external side effect. | Local harness approval is not enterprise authorization; external effects go through TG/AG policy with explicit scoped authority; stable tool-call identity prevents unsafe blind retry. | Hostile prompt/repository fixtures; deny unapproved operations; ambiguous-outcome recovery tests. |
| TM-07 | Direct egress bypasses gateways or exfiltrates data. | Default-deny NetworkPolicy/implementation-equivalent enforcement; named network profiles; secure integrated profile permits only required control, gateway, artifact, and DNS endpoints. | Probe provider endpoints, arbitrary Internet, private ranges, metadata, and Kubernetes API from the effective sandbox. |
| TM-08 | Malicious Workspace or vendor state escapes its mount or poisons restore. | Canonical fixed mount roots; path/symlink/mount-boundary validation; immutable checkpoint manifest and integrity digest; compatibility validation before restore; no executable trust inferred from persistence. | Traversal, symlink swap, special-file, oversized-state, digest mismatch, and cross-Session restore tests. |
| TM-09 | A malicious or substituted agent image executes with unexpected capabilities. | Immutable digest binding; operator-approved runtime/version resolution; signature/provenance verification seam; Runtime Profile constraints applied independently of image requests. | Tag-replacement rejection, digest mismatch, revoked-version, effective-spec, and provenance-policy tests. |
| TM-10 | Secrets or sensitive content leak through events, logs, traces, metrics, errors, checkpoints, or artifacts. | Data classification; allowlisted metadata; recursive redaction; bounded payloads; explicit chain-of-thought exclusion; execution-secret cleanup; access-controlled retention. | Canary-secret tests across all sinks, nested structures, errors, panic paths, checkpoint, and artifact publication. |
| TM-11 | Request replay, confused deputy, or caller-identity injection expands authority. | Authenticate transport credentials; derive principal/tenant from verified claims; ignore or reject identity asserted only in JSON; audience/scope/expiry binding; mutation idempotency digest and ownership. | Wrong audience/issuer/tenant, duplicate-key, concurrent replay, delegation, and idempotency-conflict tests. |
| TM-12 | Races among completion, cancellation, timeout, crash, and retry create multiple winners. | Explicit state machines; transactional terminal transition; one active mutable Execution per Session; monotonic fence; idempotent external outcome reporting. | Concurrency and property tests covering every terminal race and delayed delivery ordering. |
| TM-13 | Resource exhaustion harms the control plane or other tenants. | Runtime resource limits; quotas; request/event bounds; backpressure; bounded queues and timeouts; connection and reconciliation limits; no unbounded sandbox-controlled labels. | CPU, memory, ephemeral storage, process, event flood, SSE slow-reader, oversized-message, and acquisition-storm tests. |
| TM-14 | Control-plane, database, Kubernetes, gateway, or storage outage causes unsafe recovery. | Durable desired state; transactional outbox; leases/claims; idempotent reconciliation; fail closed on unknown authority; classify ambiguous external effects; bounded retry with jitter. | Crash and partition injection at acquisition, start, checkpoint, suspend, completion, and external-call boundaries. |
| TM-15 | Orphan cleanup deletes a live resource or preserves sensitive residue. | Stable bindings; generation-aware ownership metadata; two-phase/confirmatory cleanup; idempotent deletion; warm-pool sanitation; retention policy. | Concurrent cleanup/reacquire tests, stale binding tests, and residue inspection before reuse. |
| TM-16 | Supply-chain or operator configuration silently weakens isolation. | Pinned supported versions; least-privilege deployment; validated Runtime Profile resolution snapshot; admission/effective-state checks; auditable configuration changes. | Clean-deployment conformance, version drift, forbidden-field, downgrade, and effective-runtime tests. |

## Security invariants

1. The Sandbox MUST NOT be an authority source.
2. Long-lived provider, downstream, cluster, and control-plane credentials MUST NOT enter the Sandbox.
3. Every mutable Attempt action MUST match the current Session execution generation and current Attempt.
4. A Session MUST have at most one mutable Execution.
5. Revoked, expired, cancelled, completed, or replaced authority MUST fail closed and MUST NOT be reusable after resume.
6. Persistent Workspace or vendor state MUST NOT imply persistent execution authority.
7. Network policy and runtime isolation MUST be enforced outside the compromised sandbox.
8. External side effects MUST be authorized at a trusted gateway; harness-local consent is insufficient.
9. Recovery MUST preserve fencing and MUST NOT blindly retry an operation with an ambiguous external outcome.
10. Sensitive content MUST NOT be emitted to a less restricted data sink merely because the sandbox supplied it as metadata.

## Availability and denial of service

The platform limits rather than eliminates denial of service by authorized tenants. A compromised sandbox can exhaust its own granted resources and may generate work up to externally enforced quotas. AR MUST prevent that load from becoming unbounded control-plane memory, database growth, event fanout, reconciliation work, or cluster acquisition. Regional infrastructure failure and privileged administrator denial of service are operational risks handled by deployment and recovery design rather than sandbox controls.

## Out of scope and dependencies

- Preventing a legitimately authorized user from disclosing data they are entitled to read.
- Vulnerabilities in the underlying hardware, hypervisor, kernel, Kubernetes, Kata, CSI, registry, database, or cloud control plane beyond selecting supported versions, hardening, patching, and defense in depth.
- Compromise of trusted AG, LLMGW, TG, GR, AR, PostgreSQL, registry, storage, or cluster-administrator credentials. These remain important system threats but require component-specific threat models.
- Model alignment or correctness. GR and gateway policy reduce defined risks but do not make model output trustworthy.

## Residual risk and acceptance

Strong isolation reduces but does not prove absence of sandbox escape. Trusted infrastructure operators can access control-plane or storage data unless the deployment adds stronger encryption and separation. Standalone direct-egress profiles intentionally accept greater exfiltration risk and MUST be visibly distinct from secure integrated profiles. Storage snapshots and artifacts may contain hostile or sensitive tenant data and remain access-controlled even after a Session closes.

A production Runtime Profile is not considered secure merely because its desired manifest is correct. Release evidence MUST inspect effective runtime, network, credential, storage, and recovery behavior in representative Kubernetes and Kata environments.

## Review triggers

Review and version this threat model when adding a sandbox provider, transport, Runtime Profile or network class, harness adapter, persistent path, external integration, credential type, multi-cluster boundary, warm-pool reuse, privileged capability, or new authoritative state store. Security incidents and material upstream isolation changes also trigger review.

