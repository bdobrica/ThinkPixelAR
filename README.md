# ThinkPixelAR

ThinkPixelAR is an open-source, vendor-neutral **Agent Runtime** for durable, stateful AI-agent Sessions on isolated, disposable compute.

It owns Session continuity, bounded Execution materialization, sandbox lifecycle, persistent Workspace attachment, harness adaptation, recovery, and normalized runtime events. Kubernetes Agent Sandbox is the initial execution substrate, with Kata Containers preferred for agents that execute untrusted code.

> **Agents are untrusted. Authority lives outside the agent. Compute is disposable. State is durable. Runtime boundaries are replaceable.**

## Status

Architecture, engineering foundations, persistence, the homelab Kubernetes/Kata
substrate, and the initial `thinkpixel-agentd` control/transport path are
implemented. Phase 5 is active: the Codex runtime is pinned and packaged, and
the immediate target is one real Codex turn inside the existing Kata-backed
sandbox.

No broadly production-qualified runtime is available yet. The current priority
is a narrow, reproducible demo/RC vertical slice rather than independent
completion of every planned qualification gate.

Cross-repository development priority is defined by the ThinkPixel
[development alignment](https://github.com/bdobrica/ThinkPixel/blob/main/docs/development/ALIGNMENT.md).
It may change PLAN/TODO sequencing, but does not override accepted ADRs,
published contracts, component ownership, or security invariants.

## First milestone

The immediate milestone is to make the runtime thesis visible end-to-end:

1. run Codex through `thinkpixel-agentd` inside a Kata-backed sandbox;
2. expose Session -> Execution -> events -> result;
3. persist the Workspace and required Codex state;
4. destroy the original sandbox;
5. reconstruct execution on fresh compute and continue the same Session;
6. route model access through ThinkPixelLLMGW;
7. consume governed Run authority from ThinkPixelAG;
8. perform one visible governed GitHub operation through ThinkPixelTG.

The first RC may intentionally support one harness, one sandbox/runtime path,
one storage strategy, one model route, and one governed tool integration.
Broader qualification belongs to release promotion.

Temporal remains intentionally excluded from this path. AR uses durable state,
idempotent operations, fencing, and bounded reconciliation.

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
boundaries in `PLAN.md`. Install the pinned OpenAPI tooling
with `make deps` and install `protoc 3.21.12` (the distro `protobuf-compiler`
package where it provides that version), then run the stable local/CI gate with
`make verify`. `make generate` regenerates OpenAPI and agentd Protobuf artifacts. Use
`make help` to list the focused commands. The full gate downloads the exactly
pinned Go analysis tools and current vulnerability database, so it requires
network access on a clean cache. Build output is written below `.cache/bin`.
These checks are not runtime or release qualification.
GitHub Actions runs the same baseline gate and the two hardened image smoke
targets with read-only repository permissions and isolated, non-persistent
dependency caches.

For local database work, Docker Compose provides the pinned PostgreSQL
development service. Start it with `make db-up` and stop it with `make db-down`;
the named volume is retained. Its loopback-only port and published credentials
are development-only. The default URL is
`postgres://thinkpixelar:thinkpixelar-development-only@127.0.0.1:55432/thinkpixelar`;
set `THINKPIXELAR_POSTGRES_PORT` to change the host port. Schema changes run
separately through `make migrate` and are never applied automatically by API
replicas.

Build the baseline service container with `make image`. Run `make image-smoke`
to verify that it starts as a non-root process with a read-only filesystem and
answers `/livez`. Override the local tag with `IMAGE=registry/name:tag`.
Build the distinct sandbox-supervisor baseline with `make agentd-image` and
smoke-test it with `make agentd-image-smoke`; override its tag with
`AGENTD_IMAGE=registry/name:tag`. This image contains only `thinkpixel-agentd`,
not a vendor harness. It now requires [read-only bootstrap configuration](docs/operations/agentd.md)
and waits for transport identity/admission composition. The
[authenticated transport adapter](docs/operations/agentd-transport.md) is implemented. Vendor agent images remain
separate Phase 5 artifacts.

Typed process configuration supports strict JSON files and environment
overrides with production-safe validation and secret-redacted diagnostics.
See the [configuration reference](docs/configuration.md) for precedence,
defaults, and supported variables.

Kubernetes adapter connection settings are documented in the
[Kubernetes configuration reference](docs/kubernetes-configuration.md). Homelab
setup is documented in the [Agent Sandbox](docs/operations/agent-sandbox.md),
[Kata](docs/operations/kata.md) and [bounded scratch](docs/operations/bounded-scratch.md) runbooks.

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

ThinkPixelAR is the Agent Runtime component of the broader [ThinkPixel](https://github.com/bdobrica/ThinkPixel) platform.

Platform architecture, component responsibilities, integration status, and cross-repository development priorities are maintained centrally:

- [ThinkPixel overview](https://github.com/bdobrica/ThinkPixel)
- [Platform development guidance](https://github.com/bdobrica/ThinkPixel/tree/main/docs/development)
- [Current development alignment](https://github.com/bdobrica/ThinkPixel/blob/main/docs/development/ALIGNMENT.md)

AR remains independently usable. ThinkPixel integrations are implemented through explicit adapters and versioned contracts rather than by importing other components' internal state or implementation details.

## License

Licensed under the terms in [LICENSE](LICENSE).

Homelab operators: [Kata installation](docs/operations/kata.md) and
[RC profiles, limits, and infrastructure](docs/operations/rc-infrastructure.md).
