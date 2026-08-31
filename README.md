# ThinkPixelAR

ThinkPixelAR is an open-source, vendor-neutral **Agent Runtime** for durable, stateful AI-agent Sessions on isolated, disposable compute.

It owns Session continuity, bounded Execution materialization, sandbox lifecycle, persistent Workspace attachment, harness adaptation, recovery, and normalized runtime events. Kubernetes Agent Sandbox is the initial execution substrate, with Kata Containers preferred for agents that execute untrusted code.

> **Agents are untrusted. Authority lives outside the agent. Compute is disposable. State is durable. Runtime boundaries are replaceable.**

## Status

ThinkPixelAR is in architecture and implementation planning. Phase 0 contracts and decisions are complete; implementation work is tracked in [TODO.md](TODO.md) and sequenced in [PLAN.md](PLAN.md).

No release-qualified runtime is available yet. Candidate dependency versions in the [compatibility baseline](docs/supported-versions.md) are not claims of completed deployment or security qualification.

## First milestone

The initial vertical slice targets a Go/PostgreSQL control plane, Kubernetes Agent Sandbox with a Kata-backed secure Runtime Profile, durable Workspace and checkpoint state, `thinkpixel-agentd`, and Codex App Server as the first structured `HarnessAdapter`. Standalone bounded authority and replaceable ThinkPixel integrations remain separate adapters.

Temporal is intentionally excluded from the MVP and release candidate. AR uses durable state, idempotent operations, fencing, and bounded reconciliation.

## Key concepts

- A **Session** is durable continuity and may outlive every process or sandbox that served it.
- A **Run** is a bounded governed operation owned by ThinkPixelAG in integrated mode.
- An **Execution** materializes one bounded authorized operation within a Session.
- An **Attempt** is one physical try; only the current fenced Attempt may mutate authoritative state.
- A **Sandbox** is replaceable, untrusted compute—not the Session itself.
- A **Workspace** is durable filesystem state; a **Checkpoint** binds restorable state and integrity metadata without credentials or live authority.

## Security boundary

AR keeps durable control state, authorization decisions, and long-lived credentials outside the untrusted sandbox. Every Execution receives finite, execution-scoped authority; Session generation and Attempt fences reject stale compute. Standalone `LocalAuthority` does not claim the governance guarantees of ThinkPixelAG, and there is no permissive fallback when an integrated authority is unavailable.

See the [repository alignment contract](ALIGNMENT.md), [system context](docs/architecture/system-context.md), and [threat model](docs/security/threat-model.md).

## Development

Development uses the exact Go version in `.go-version` (currently `1.26.7`).
The package scaffold follows the domain, application, port, and adapter
boundaries in `PLAN.md`; command and deployment directories are reserved for
their later engineering-foundation items. Install the pinned OpenAPI tooling
with `make deps`, then run the stable local/CI gate with `make verify`. Use
`make help` to list the focused commands. The full gate downloads the exactly
pinned Go analysis tools and current vulnerability database, so it requires
network access on a clean cache. Build output is written below `.cache/bin`.
These checks are not runtime or release qualification.

For local database work, Docker Compose provides the pinned PostgreSQL
development service. Start it with `make db-up` and stop it with `make db-down`;
the named volume is retained. Its loopback-only port and published credentials
are development-only. The default URL is
`postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:55432/thinkpixelar`;
set `THINKPIXELAR_POSTGRES_PORT` to change the host port. Schema changes run
separately through `make migrate` and are never applied automatically by API
replicas. The migration engine and first schema arrive in DB-001.

Build the baseline service container with `make image`. Run `make image-smoke`
to verify that it starts as a non-root process with a read-only filesystem and
answers `/livez`. Override the local tag with `IMAGE=registry/name:tag`.
Build the distinct sandbox-supervisor baseline with `make agentd-image` and
smoke-test it with `make agentd-image-smoke`; override its tag with
`AGENTD_IMAGE=registry/name:tag`. This image contains only `thinkpixel-agentd`,
not a vendor harness. Vendor agent images remain separate Phase 5 artifacts.

Typed process configuration supports strict JSON files and environment
overrides with production-safe validation and secret-redacted diagnostics.
See the [configuration reference](docs/configuration.md) for precedence,
defaults, and supported variables.

## Documentation

- [Documentation index](docs/README.md)
- [Architecture decisions](docs/adr/README.md)
- [Architecture and trust boundaries](docs/architecture/README.md)
- [Domain and integration contracts](docs/contracts/README.md)
- [HTTP API and OpenAPI](docs/api/README.md)
- [Security](docs/security/README.md)
- [Operations](docs/operations/README.md)
- [Verification evidence](docs/evidence/README.md)
- [Supported-version baseline](docs/supported-versions.md)
- [Implementation plan](PLAN.md) and [work ledger](TODO.md)

## ThinkPixel platform

This project is part of the **ThinkPixel** family: a modular, vendor-neutral set of components for building governed enterprise AI-agent platforms.

Each component is independently useful. The complete platform is a composition of replaceable services connected through versioned contracts; no component requires the full stack in order to be deployed.

| Component | Role |
|---|---|
| [ThinkPixelAG](https://github.com/bdobrica/ThinkPixelAG) | Agent governance and lifecycle control plane: agent/run authority, policy decisions, resource envelopes, approvals, revocation, and trusted governance state. |
| [ThinkPixelAR](https://github.com/bdobrica/ThinkPixelAR) | Agent runtime: durable Sessions, isolated/disposable execution, harness adaptation, recovery, and runtime events. |
| [ThinkPixelWS](https://github.com/bdobrica/ThinkPixelWS) | Durable roaming Workspaces: persistent work context, immutable generations, materializations, snapshots, forks, and source provenance. |
| [ThinkPixelMEM](https://github.com/bdobrica/ThinkPixelMEM) | Long-term agent memory: governed learned context, provenance, temporal revisions, retrieval, correction, and forgetting. |
| [ThinkPixelMP](https://github.com/bdobrica/ThinkPixelMP) | Marketplace and software supply-chain plane for Skills, runtimes, MCP servers, agent bundles, and other immutable agentic artifacts. |
| [ThinkPixelTG](https://github.com/bdobrica/ThinkPixelTG) | Tool gateway and policy-enforcement point for governed tool calls, downstream credentials, side effects, idempotency, and tool evidence. |
| [ThinkPixelLLMGW](https://github.com/bdobrica/ThinkPixelLLMGW) | LLM gateway for provider abstraction, model routing, credentials, budgets, accounting, and model-access policy enforcement. |
| [ThinkPixelGR](https://github.com/bdobrica/ThinkPixelGR) | Guardrails evaluator for model, tool, retrieval, and ingestion content. It returns findings/decisions; the calling gateway or service enforces them. |

### Intended composition

```mermaid
flowchart LR
    C[Clients / IDEs / automation] --> AG[ThinkPixelAG<br/>governance]
    AG -->|governed Run + resource envelope| AR[ThinkPixelAR<br/>runtime]

    MP[ThinkPixelMP<br/>qualified artifacts] -->|immutable resolutions| AG
    MP -.->|runtime / skill / environment refs| AR

    AR <-->|materialize / commit work context| WS[ThinkPixelWS<br/>workspaces]

    AR -->|model calls + governed context| LLMGW[ThinkPixelLLMGW<br/>LLM gateway]
    LLMGW --> MODEL[Model providers]
    LLMGW -.->|pre_model / post_model| GR[ThinkPixelGR<br/>guardrails]

    AR -->|governed tool calls| TG[ThinkPixelTG<br/>tool gateway]
    TG --> SYS[Enterprise systems]
    TG <-->|authorize / approve / meter| AG
    TG -.->|pre_tool / post_tool| GR

    AR <-->|ContextPacks / execution evidence| MEM[ThinkPixelMEM<br/>memory]
    WS -->|source provenance| MEM
    TG -->|verified outcomes| MEM
    MEM <-->|MemoryGrants / memory policy| AG
    MEM -.->|extraction / embeddings / reranking| LLMGW
    MEM -.->|retrieval / ingestion evaluation| GR
    MEM -.->|reviewed procedure candidates| MP
```

The diagram describes the **target integration model**, not a claim that every edge is implemented in every current release.

### Integration rules

The platform follows a few cross-component rules:

- **Authority does not emerge from content.** Marketplace metadata, Skills, Workspace membership, retrieved memory, model output, or a guardrail `allow` decision cannot grant permissions that the governed Run does not already have.
- **State has one authoritative owner.** Components exchange references and versioned messages; they do not read or write another component's database directly.
- **Integrations are adapters, not domain dependencies.** A ThinkPixel integration should be configurable and replaceable with a contract-compatible alternative.
- **Cross-component identity is explicit.** Where relevant, requests should carry stable governed context such as tenant, principal, agent, Run, Session/Workspace references, immutable artifact digests, and trace context.
- **Public integration contracts are versioned.** OpenAPI/JSON Schema/protobuf or another explicit wire contract is preferred over importing another repository's internal types.
- **Vendor-specific behavior stays behind adapters.** Model providers, agent harnesses, storage systems, registries, policy engines, and execution substrates must not become platform-wide domain contracts.

### Planned integration points

| Integration | Intended contract |
|---|---|
| **AG → AR** | AG admits a Run and supplies its authority/resource context; AR executes it and must not enlarge that authority. Revocation, lease, and fencing state flow back into runtime enforcement. |
| **MP → AG / AR / WS** | MP resolves qualified artifacts to immutable identities/digests. AG decides whether they may be used; AR/WS consume the resolved runtime, Skill, or environment references. Qualification is not authorization. |
| **AR ↔ WS** | AR materializes a durable Workspace generation into disposable execution and returns committed/checkpointed work to WS. Session identity remains owned by AR; Workspace identity remains owned by WS. |
| **AR → LLMGW** | Agent model calls go through LLMGW with governed Run/tenant context. Provider credentials and provider-specific routing stay outside the harness. |
| **LLMGW ↔ GR** | LLMGW will support an optional configured GR endpoint/profile mapping. It invokes `pre_model` before provider dispatch and `post_model` before releasing model output, then enforces GR's decision/transformation. GR remains optional and replaceable; its wire API is the contract. |
| **AR → TG** | Harness tool calls cross TG rather than reaching governed enterprise systems directly. TG owns credential brokerage, idempotency/side-effect handling, and trusted tool evidence. |
| **TG ↔ AG** | TG asks AG (or a contract-compatible authorizer) whether the current governed Run may perform the exact operation and obtains action-scoped approval when required. TG returns trusted metering/evidence. |
| **TG ↔ GR** | TG invokes `pre_tool` and `post_tool` evaluation when configured and enforces the result. A GR allow never overrides an AG authorization denial. |
| **AR / WS / TG → MEM** | Execution history, Workspace provenance, and verified tool outcomes may become evidence for learned memory. MEM does not become the source of truth for those upstream systems. |
| **AG ↔ MEM** | AG supplies Run-scoped memory authority (for example MemoryGrants); MEM enforces it for reads/writes and returns structured ContextPacks. |
| **MEM ↔ LLMGW / GR** | MEM may use LLMGW for extraction/embedding/reranking and GR for ingestion/retrieval inspection while keeping canonical memory state independent from either service. |
| **MEM → MP** | Learned procedure candidates may be reviewed and promoted through MP into qualified reusable Skills; learning does not silently become trusted executable behavior. |

Project-specific implementation status, supported versions, and release qualification belong in each project's own documentation.

## License

Licensed under the terms in [LICENSE](LICENSE).
