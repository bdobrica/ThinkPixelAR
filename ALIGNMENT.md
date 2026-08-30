# ThinkPixelAR alignment

This file defines how **ThinkPixelAR** stays aligned with the wider ThinkPixel platform. It is a repository-local alignment guide, not a replacement for accepted ADRs. If this file conflicts with an accepted ADR, the ADR wins and this file should be corrected.

## Platform role

ThinkPixelAR is the **agent runtime and execution plane**. It preserves durable Session continuity and materializes bounded, authorized work onto isolated, disposable compute without becoming the authority for governed Runs, model access, enterprise tools, guardrails, marketplace qualification, Workspace source data, or learned memory.

## This repository owns

- Durable Session identity and lifecycle, runtime Executions and physical Attempts.
- Session execution generations, Attempt fencing, runtime reconciliation, recovery, and normalized runtime events.
- Sandbox lifecycle through replaceable providers and harness lifecycle through replaceable adapters.
- Runtime Profile resolution and immutable agent-runtime binding for a Session.
- Checkpoint/restore coordination and runtime attachment to durable Workspace generations.
- Runtime-local persistence and evidence needed to materialize and recover execution safely.

## This repository does not own

- Governed Run admission, agent/version approval, leases, revocation, or resource authority — ThinkPixelAG.
- Marketplace qualification, artifact promotion, or supply-chain trust — ThinkPixelMP.
- Authoritative durable Workspace contents, generations, source provenance, or roaming work context — ThinkPixelWS when configured.
- Long-term learned memory or knowledge/RAG indexing — ThinkPixelMEM.
- Live enterprise tool authorization, downstream credentials, or side-effect truth — ThinkPixelTG.
- LLM provider routing, provider credentials, accounting, or model-access policy — ThinkPixelLLMGW.
- Content guardrail policy or evaluation — ThinkPixelGR.
- Kubernetes Agent Sandbox, Kata, storage-provider, or harness implementation details as public AR domain concepts.

Standalone adapters may provide bounded local substitutes required to operate AR independently. They must not claim the governance or ownership guarantees of the corresponding ThinkPixel service.

## Integration obligations

| Peer | Boundary |
|---|---|
| **ThinkPixelAG** | Consume a versioned, finite Run authority/grant and enforce its lease, revocation, resource envelope, and fences. AR may narrow that authority but never enlarge it or fail open to standalone authority. |
| **ThinkPixelMP** | Consume exact immutable artifact resolutions and qualification evidence selected through governance. Marketplace metadata or qualification never authorizes an Execution. |
| **ThinkPixelWS** | Materialize and commit referenced Workspace generations through a versioned provider contract. AR owns Session/runtime attachment; WS owns authoritative mutable work state when configured. |
| **ThinkPixelMEM** | Exchange only explicitly governed ContextPacks or execution evidence through a versioned adapter. Retrieved or learned content never expands Run authority or becomes AR Session truth. |
| **ThinkPixelTG** | Route governed enterprise tool calls through TG using Execution/Run context. Long-lived downstream credentials and authoritative side-effect records stay outside the Sandbox and AR. |
| **ThinkPixelLLMGW** | Route model calls through LLMGW when configured. Provider credentials, provider-specific routing, and authoritative accounting stay outside the harness and AR. |
| **ThinkPixelGR** | Supply the applicable governed context to the enforcing gateway/service when guardrail evaluation is configured. A guardrail result is policy input, not new authority. |

All ThinkPixel integrations must remain optional/configurable where standalone operation is a product goal. Integration code belongs behind a port/adapter or equivalent boundary; another repository's database or `internal` packages are never an integration API.

## Shared ThinkPixel invariants

- Agents, harnesses, generated code, repository content, dependencies, and `thinkpixel-agentd` are untrusted application logic; authority lives in trusted control/enforcement services.
- A component may **narrow** effective authority but must not manufacture authority from content, metadata, memory, Skills, Workspace membership, model output, or guardrail results.
- Durable state has a single authoritative owner. Other stores are caches, projections, replicas, evidence, or referenced source data unless an accepted ADR says otherwise.
- Cross-component references use stable/versioned identifiers and immutable digests where identity matters.
- Vendor/provider-specific types stay behind adapters and do not leak into AR's core domain or public API.
- Public integration behavior is contract-first: versioned OpenAPI/JSON Schema/protobuf or another explicit wire contract, plus compatibility tests.
- Security-relevant transformations must not reuse authorization/approval decisions made for materially different input.
- Evidence and telemetry must be correlated without turning logs into a store for credentials, prompts, model output, Workspace contents, or other unnecessary sensitive payloads.

## Repository conventions

- `README.md` is an entry point, not the design specification. Keep it focused on purpose, status, a minimal usage path, key concepts, and links.
- Do not duplicate `PLAN.md` in the README. `PLAN.md` is temporary implementation intent; `TODO.md` is the ordered execution/release ledger.
- As plan decisions become real, move durable rationale into `docs/adr/` and durable reference material into `docs/`.
- Accepted ADRs are immutable in meaning and are superseded with a new ADR.
- Prefer the existing `docs/README.md` index and the logical categories `adr`, `architecture`, `contracts`, `security`, `operations`, and `evidence`.
- Prefer Mermaid for diagrams and relative links for repository-local documentation.
- As executable code matures, provide one stable root developer/CI entry point. For this Go service, the planned convention is a Makefile with focused targets and an aggregate `verify` target.
- Public API and schema changes must update the machine-readable contract, implementation, compatibility documentation, and tests atomically.
- Dependency additions, new infrastructure authorities, and new cross-component source dependencies require explicit repository-local justification; consequential choices require an ADR.

## Repository-specific alignment

- Preserve the distinction: AG owns a governed Run; AR materializes it as an Execution; an Attempt is one replaceable physical try inside a disposable Sandbox.
- Preserve the invariant that a Session is durable but authority is not. Every Execution receives fresh finite authority, and stale generations/Attempts are fenced.
- Preserve immutable Session runtime binding. Marketplace resolution, mutable tags, or adapter discovery must not silently change the runtime of existing Session state.
- Preserve the outbound-only, mutually authenticated `thinkpixel-agentd` transport. `agentd` shares the Sandbox trust zone and is not an authorization boundary.
- Keep Kubernetes Agent Sandbox, Kata, CSI/storage, harness, authority, and future Workspace-service behavior behind explicit ports/adapters.
- Keep Session and runtime lifecycle truth in AR's PostgreSQL model. Provider resources, reconciler leases, harness state, and any future workflow engine are not additional public lifecycle authorities.
- Keep Temporal and other durable workflow engines out of the MVP/RC unless ADR-0004 is superseded using measured evidence and a migration plan.
- Advertise Session fork only for a qualified WorkspaceProvider/HarnessAdapter combination as required by ADR-0003; capability discovery never grants permission.
- Do not claim runtime, Kubernetes/Kata, provider, or security qualification from documentation-only validation or candidate version pins.

## Structure guidance

- The current documentation-first layout is appropriate for Phase 0. `docs/api/` is the existing public API location and may remain until a justified implementation change moves machine-readable contracts to root `api/`.
- Add the planned `cmd`, `internal`, migrations, deployment, and test structure incrementally during the corresponding `TODO.md` phases rather than scaffolding speculative code.
- Add the root Makefile and aggregate `make verify` during the engineering-foundation phase; generated OpenAPI/schema drift belongs in that gate.
- Keep sandbox providers, authority integrations, Workspace providers, harnesses, gateways, and vendor protocols outside the core Session/Execution domain.

## Definition of an aligned change

A change is aligned when it preserves AR's ownership boundary, follows accepted ADRs and contracts, keeps integrations replaceable, updates durable documentation with behavior, and passes the repository's documented verification gates. Changes to a cross-repository boundary should include a versioned wire contract and compatibility/conformance coverage rather than relying only on prose.
