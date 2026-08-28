# System context and trust boundaries

Status: Normative Phase 0 architecture contract.

ThinkPixelAR (AR) preserves durable agent Session continuity while materializing bounded work onto disposable, isolated compute. Authority, durable control state, credentials, and enforcement remain outside the agent sandbox.

## System context

```mermaid
flowchart LR
    CLIENTS[Clients<br/>IDEs, users, automation]

    subgraph THINKPIXEL[ThinkPixel control and gateway plane]
        AG[ThinkPixelAG<br/>Run authority and governance]
        AR[ThinkPixelAR control plane<br/>Sessions, Executions, reconciliation]
        LLMGW[ThinkPixelLLMGW<br/>model gateway]
        TG[ThinkPixelTG<br/>tool and source gateway]
        GR[ThinkPixelGR<br/>guardrail evaluation]
        PG[(PostgreSQL<br/>authoritative AR metadata)]
    end

    subgraph EXECUTION[Untrusted execution plane]
        AGENTD[thinkpixel-agentd]
        HARNESS[Agent harness<br/>Codex and future adapters]
    end

    KAS[Kubernetes Agent Sandbox<br/>sandbox lifecycle]
    KATA[Kata runtime<br/>strong isolation]
    REGISTRY[OCI registry<br/>immutable agent images]
    STORAGE[(Durable storage<br/>Workspace, vendor state, artifacts)]
    MODEL[External model providers]
    TOOLS[External tool providers<br/>SCM, issue trackers, APIs]

    CLIENTS -->|authenticated REST / SSE| AR
    AR <-->|governed Run admission,<br/>lease, fence, outcome| AG
    AR -->|durable transactions| PG
    AR -->|sandbox desired state| KAS
    KAS -->|select RuntimeClass| KATA
    REGISTRY -->|image by digest| KAS
    STORAGE <-->|attach, checkpoint,<br/>restore| KAS
    AR <-->|sandbox-scoped authenticated channel| AGENTD
    AGENTD -->|local structured protocol| HARNESS
    HARNESS -->|execution-scoped model authority| LLMGW
    HARNESS -->|execution-scoped tool authority| TG
    LLMGW <-->|policy evaluation| GR
    TG <-->|policy evaluation| GR
    LLMGW -->|provider credentials remain here| MODEL
    TG -->|downstream credentials remain here| TOOLS
```

### Responsibility boundaries

- Clients request runtime operations but cannot assert trusted tenant, caller, Run, lease, or fencing identity in request payloads.
- AR is authoritative for Session, Execution, Attempt, checkpoint-reference, and reconciliation state. PostgreSQL is the durable source of that state.
- AG is authoritative for governed Runs, resource envelopes, revocation, leases, and fencing in integrated mode.
- Kubernetes Agent Sandbox is the initial lifecycle substrate; Kata is selected through an operator-controlled Runtime Profile and RuntimeClass mapping.
- `thinkpixel-agentd`, the harness, repository content, generated code, dependencies, and child processes share the untrusted sandbox trust zone.
- LLMGW and TG keep long-lived provider and downstream credentials outside the sandbox. GR participates through the appropriate gateway boundary.
- The registry and durable storage are infrastructure dependencies. Image digests and checkpoint integrity bind consumed content to persisted AR state.

## Trust-boundary view

```mermaid
flowchart TB
    subgraph EXTERNAL[External callers and services]
        CLIENT[Client]
        MODEL[Model provider]
        TOOL[Tool provider]
    end

    subgraph TRUSTED[Trusted control, gateway, and data plane]
        AR[ThinkPixelAR]
        AG[ThinkPixelAG]
        LLMGW[ThinkPixelLLMGW]
        TG[ThinkPixelTG]
        GR[ThinkPixelGR]
        PG[(PostgreSQL)]
        REGISTRY[OCI registry]
        STORAGE[(Durable storage)]
        K8S[Kubernetes control plane<br/>Agent Sandbox controller]
    end

    subgraph ISOLATION[Kata isolation boundary]
        subgraph SANDBOX[Untrusted Agent Sandbox]
            AGENTD[thinkpixel-agentd]
            HARNESS[Harness]
            CODE[Repository and generated code]
            DEPS[Dependencies and child processes]
        end
    end

    CLIENT -->|authenticated API| AR
    AR <-->|authenticated authority contract| AG
    AR --> PG
    AR -->|authenticated Kubernetes API| K8S
    K8S -->|verified image digest| REGISTRY
    K8S <-->|bounded volume operations| STORAGE
    AR <-->|sandbox identity + bounded protocol| AGENTD
    AGENTD --> HARNESS
    HARNESS --> CODE
    CODE --> DEPS
    HARNESS -->|short-lived scoped credential| LLMGW
    HARNESS -->|short-lived scoped credential| TG
    LLMGW --> GR
    TG --> GR
    LLMGW --> MODEL
    TG --> TOOL
```

The arrow from AR into the sandbox is not a transfer of trust. Messages from `agentd` and the harness are observations that AR validates against persisted generation, Attempt, authority, and lifecycle state. The Sandbox MUST NOT receive Kubernetes credentials, cloud metadata credentials, provider API keys, downstream enterprise credentials, or authority capable of outliving its current Execution.

## Standalone deployment

In standalone mode, `LocalAuthority` replaces AG at the `RunAuthority` port. It supplies bounded operator-configured grants but does not claim enterprise governance equivalence. LLMGW, TG, and GR are optional; a deliberately configured standalone Runtime Profile may permit direct egress. The sandbox remains untrusted and AR/PostgreSQL remain outside it.

## Failure and replacement boundary

```mermaid
flowchart LR
    STATE[(PostgreSQL state)] --> AR[AR reconciler]
    CHECKPOINT[(Workspace and checkpoint)] --> AR
    AUTH[Current authority state] --> AR
    OBSERVED[Observed sandbox and harness state] --> AR
    AR -->|fenced idempotent actions| OLD[Current Sandbox]
    OLD -. loss, crash, suspension .-> GONE[Disposable compute removed]
    AR -->|new Attempt and sandbox identity| NEW[Replacement Sandbox]
    CHECKPOINT -->|restore| NEW
```

Loss of a Pod, Kata VM, sandbox, `agentd`, harness process, node, or AR replica does not by itself destroy the Session. Recovery is allowed only when durable state, checkpoint integrity, current authority, and fencing rules permit it.

## Diagram maintenance rules

- Public and domain documentation uses AR-neutral terms; Kubernetes CRD types stay inside the sandbox adapter boundary.
- New external data or control flows MUST be added to both the system-context and trust-boundary diagrams.
- A new path from the sandbox to a trusted or external service requires a security review covering identity, audience, scope, expiry, replay, revocation, egress, and durable-data exposure.

