# ThinkPixelAR Release-Candidate TODO

This is the chronological implementation checklist for ThinkPixelAR.

Execute the first unchecked item whose dependencies are complete.

An item is checked only after its acceptance evidence passes.

Follow the coding-agent and commit protocol in `PLAN.md` after every completed implementation item.

Status notation:

- `[ ]` pending
- `[x]` implemented and verified

Completion metadata format:

    — completed YYYY-MM-DD, commit <sha>, evidence: <commands/artifacts>

---

## Phase 0 — Decisions, threats, and contracts

- [x] ARC-001 Create `docs/` structure and ADR template covering status, context, decision, alternatives, consequences, security, operations, compatibility, and references. — completed 2026-08-28, commit `17cef58`, evidence: `find docs -type f`, ADR section validation
- [x] ARC-002 Write system-context and trust-boundary diagrams covering clients, AR control plane, PostgreSQL, Kubernetes Agent Sandbox, Kata runtime, `agentd`, harness, AG, LLMGW, TG, GR, registry, storage, and external providers. — completed 2026-08-28, commit `473fbf8`, evidence: component coverage and Mermaid fence validation in `docs/architecture/system-context.md`
- [x] ARC-003 Write the primary threat model assuming complete compromise of harness, generated code, repository code, dependencies, and `agentd`. — completed 2026-08-28, commit `f1a7b59`, evidence: threat/control/verification coverage in `docs/security/threat-model.md`; `git diff --check`
- [x] ARC-004 Define data-classification/redaction rules for prompts, model output, repository content, vendor state, Workspace contents, credentials, events, logs, traces, checkpoints, and artifacts. — completed 2026-08-28, commit `93a660c`, evidence: required-category matrix and redaction-contract validation in `docs/security/data-classification.md`; `git diff --check`
- [x] ARC-005 Define the glossary and normative distinction between Session, Run, Execution, Attempt, Sandbox, harness process, Workspace, Checkpoint, Runtime Profile, and ExecutionGrant. — completed 2026-08-28, commit `cafcdd1`, evidence: canonical-term and identity/lifetime matrix validation in `docs/contracts/glossary.md`; `git diff --check`
- [x] ARC-006 Formalize Session state machine including creation, ready, active, idle, suspended, degraded, close, recovery, and illegal transitions. — completed 2026-08-28, commit `d45a3d7`, evidence: state/edge/illegal-transition validation in `docs/contracts/session-state-machine.md`; Mermaid fence validation; `git diff --check`
- [x] ARC-007 Formalize Execution and Attempt state machines including cancellation, timeout, crash, retry, replacement, and terminal-state races. — completed 2026-08-28, commit `82bf7ee`, evidence: dual-state/required-race coverage in `docs/contracts/execution-attempt-state-machines.md`; Mermaid fence validation; `git diff --check`
- [x] ARC-008 Define the single-writer Session invariant and monotonic Session execution generation/fencing rules. — completed 2026-08-28, commit `d8822bd`, evidence: invariant/generation/fence/atomic-replacement coverage in `docs/contracts/session-single-writer-fencing.md`; Mermaid fence validation; `git diff --check`
- [x] ARC-009 Define `RunAuthority` contract and exact behavior of standalone `LocalAuthority`. — completed 2026-08-28, commit `e98a858`, evidence: interface/method/local-policy/lifecycle/conformance coverage in `docs/contracts/run-authority.md`; `git diff --check`
- [x] ARC-010 Reconcile `ThinkPixelAGAuthority` operations with the current ThinkPixelAG Run worker/lease/fencing APIs and record any required cross-project API changes. — completed 2026-08-28, commit `687bc2d`, evidence: ThinkPixelAG revision `b1678683058845b63e9188b5b60a5b01b1fcadf2`; operation/state/gap coverage in `docs/contracts/thinkpixelag-authority-integration.md`; `git diff --check`
- [x] ARC-011 Define secure caller delegation/OBO behavior between AR and AG; prohibit caller identity supplied only through untrusted JSON. — completed 2026-08-28, commit `510ef17`, evidence: subject/actor/worker, inbound verification, OBO, confused-deputy, failure, and JSON-identity prohibition coverage in `docs/security/caller-delegation.md`; `git diff --check`
- [x] ARC-012 Decide and document Session immutable agent-version binding, continuation authorization, revoked-version behavior, and future migration semantics. — completed 2026-08-28, commit `4f6dc89`, evidence: accepted `docs/adr/0001-immutable-session-runtime-binding.md`; binding/continuation/revocation/migration section validation; `git diff --check`
- [x] ARC-013 Define `AgentRuntimeSpec` schema including immutable OCI image digest, adapter kind/version, entrypoint, durable vendor paths, workspace mount, Runtime Profile, and platform requirements. — completed 2026-08-28, commit `be3861a`, evidence: JSON parse/required-field/schema-shape validation for `docs/contracts/agent-runtime-spec.schema.json`; contract coverage in `docs/contracts/agent-runtime-spec.md`; `git diff --check`
- [x] ARC-014 Define the `SandboxProvider` interface independent of Kubernetes types. — completed 2026-08-28, commit `180b23d`, evidence: interface/type/operation/state/error/reconciliation/conformance coverage and Kubernetes-leakage prohibition in `docs/contracts/sandbox-provider.md`; `git diff --check`
- [x] ARC-015 Pin and document the initially supported Kubernetes, Kubernetes Agent Sandbox, Kata, container runtime, and CSI feature/version matrix. — completed 2026-08-28, commit `83fe4f3`, evidence: upstream primary-source review dated 2026-08-28; exact baseline and KAS/Kata/containerd/CSI matrices in `docs/supported-versions.md`; required-component/version validation; `git diff --check`
- [x] ARC-016 Define Runtime Profile schema and abstract isolation classes; document the initial secure coding profile. — completed 2026-08-28, commit `54e4e5a`, evidence: JSON parse/required-section/profile-invariant validation for `docs/contracts/runtime-profile.schema.json` and `docs/profiles/coding-medium-secure.json`; isolation/resolution/security coverage in `docs/contracts/runtime-profiles.md`; `git diff --check`
- [x] ARC-017 Define `HarnessAdapter` interface, capability model, compatibility negotiation, and adapter conformance requirements. — completed 2026-08-28, commit `f33d8dc`, evidence: interface/capability/negotiation/lifecycle/event/failure/conformance coverage and Mermaid validation in `docs/contracts/harness-adapter.md`; `git diff --check`
- [x] ARC-018 Define `thinkpixel-agentd` responsibilities, trust assumptions, process model, and failure semantics. — completed 2026-08-28, commit `d14c2df`, evidence: trust/process/bootstrap/responsibility/non-responsibility/credential/failure/reconnect/verification coverage and Mermaid validation in `docs/contracts/agentd.md`; `git diff --check`
- [x] ARC-019 Select and document the initial authenticated AR↔agentd transport based on supported Kubernetes Agent Sandbox capabilities. — completed 2026-08-28, commit `0cac5f3`, evidence: accepted `docs/adr/0002-agentd-outbound-mtls-grpc-transport.md`; Agent Sandbox `v0.5.5` capability/alternative review; identity/bootstrap/rotation/replay/backpressure/failure/verification coverage; `git diff --check`
- [x] ARC-020 Define Workspace, WorkspaceGeneration, WorkspaceProvider, snapshot, clone, attach, and deletion semantics. — completed 2026-08-28, commit `b8565f1`, evidence: aggregate/generation/provider/interface/attach/snapshot/clone/delete/failure/security/conformance coverage and Mermaid validation in `docs/contracts/workspace.md`; `git diff --check`
- [x] ARC-021 Define Workspace source semantics for empty, artifact, repository snapshot/bundle, and governed ThinkPixelTG materialization. — completed 2026-08-28, commit `e08054b`, evidence: closed source union, neutral provider port, governed resolution, atomic materialization/publication, provenance/integrity, failure/security/conformance coverage in `docs/contracts/workspace-sources.md`; `git diff --check`
- [x] ARC-022 Define Checkpoint format, integrity requirements, vendor-state references, Workspace generation linkage, and explicit credential exclusions. — completed 2026-08-28, commit `c9f2bd4`, evidence: normative format/publication/integrity/compatibility/exclusion/lifecycle/failure contract and JSON Schema in `docs/contracts/checkpoint.md` and `checkpoint-manifest.schema.json`; `jq empty`; `git diff --check`
- [x] ARC-023 Define suspend/resume semantics and the minimum durable state required before compute may be released. — completed 2026-08-28, commit `26f0929`, evidence: durable release gate, suspend/resume sequences, fencing/idempotency, failure/security/conformance coverage and Mermaid validation in `docs/contracts/suspend-resume.md`; `git diff --check`
- [x] ARC-024 Define Session fork semantics and determine whether fork is RC-required or capability-gated by storage support. — completed 2026-08-28, commit `72a7282`, evidence: accepted `docs/adr/0003-capability-gated-session-fork.md`; capability decision, source/destination semantics, protocol, authority/isolation, alternatives, failure/conformance coverage; `git diff --check`
- [x] ARC-025 Define network-profile model including `none`, `thinkpixel-only`, restricted development/package mirrors, and standalone direct-egress behavior. — completed 2026-08-28, commit `d4c2fd2`, evidence: five-mode model/schema, control/data separation, selectors/DNS/enforcement/baseline-deny/failure/conformance coverage in `docs/contracts/network-profiles.md`; `jq empty`; `git diff --check`
- [x] ARC-026 Define execution-scoped credential rules, TTL/audience/binding requirements, checkpoint exclusion, and process-restart behavior between Executions. — completed 2026-08-28, commit `7712e6f`, evidence: issuer/audience/fence binding, bounded TTL/rotation/revocation, ephemeral injection, durable exclusion, per-Execution process replacement, failure/conformance coverage in `docs/security/execution-credentials.md`; `git diff --check`
- [x] ARC-027 Define local sandbox permission vs enterprise tool authorization semantics. — completed 2026-08-28, commit `c3b43b6`, evidence: action classification, local permission boundary, TG authorization/action binding, lifecycle/ambiguity, standalone behavior, failure/security/conformance coverage in `docs/contracts/permission-vs-tool-authorization.md`; `git diff --check`
- [x] ARC-028 Define normalized Runtime Event envelope, event-type registry, ordering, retention, sensitive-payload handling, and explicit chain-of-thought exclusion. — completed 2026-08-28, commit `d18b869`, evidence: envelope/schema, closed registry, transactional ordering, replay/retention, sensitive-payload and hidden-reasoning exclusion coverage in `docs/contracts/runtime-events.md`; `jq empty`; `git diff --check`
- [x] ARC-029 Define idempotency semantics for Session creation, Execution creation, sandbox acquisition, source materialization, checkpoint publication, cancellation, and close. — completed 2026-08-28, commit `0a6c909`, evidence: scoped key/digest/record contract, seven-operation semantics, ambiguity/recovery, expiry/security/concurrency verification coverage in `docs/contracts/idempotency.md`; `git diff --check`
- [x] ARC-030 Define PostgreSQL persistence model, tenant-scoping rules, optimistic versions, uniqueness constraints, outbox, and migration strategy. — completed 2026-08-28, commit `cff59b4`, evidence: logical table/constraint model, tenant-qualified access, optimistic transactions, outbox, retention/recovery, expand–migrate–contract and verification coverage in `docs/contracts/postgresql-persistence.md`; `git diff --check`
- [x] ARC-031 Draft OpenAPI 3.1 for Session, Execution, signal/cancel, suspend/resume/fork, events, runtime profiles, adapters, health, and administrative operations. — completed 2026-08-28, commit `fda4248`, evidence: `docs/api/openapi.yaml`; OpenAPI version, 17 required paths, unique operation IDs, internal reference and `git diff --check` validation
- [x] ARC-032 Define authentication, authorization, pagination, RFC 7807 errors, request IDs, W3C tracing, request limits, SSE resume/backpressure, and idempotency headers. — completed 2026-08-28, commit `be4fabc`, evidence: identity/authorization, cursors, problem taxonomy, correlation/tracing, concrete limits, concurrency and SSE replay/backpressure coverage in `docs/api/http-conventions.md`; `git diff --check`
- [x] ARC-033 Record ADR explicitly excluding Temporal from MVP/RC and document conditions that would justify adding a durable workflow engine later. — completed 2026-08-28, commit `17b0f85`, evidence: accepted `docs/adr/0004-no-temporal-in-mvp-rc.md`; reconciliation decision, ownership constraints, quantified reconsideration triggers, alternatives/security/verification coverage; `git diff --check`
- [x] ARC-034 Define target SLOs and initial capacity assumptions for API, reconciliation, sandbox acquisition, cold start, warm start, resume, checkpoint, and event streaming. — completed 2026-08-28, commit `410dfc2`, evidence: measurement rules, API/reconciliation/lifecycle/event targets, capacity envelope, budgets/recovery/load evidence in `docs/operations/slos-capacity.md`; `git diff --check`
- [x] ARC-035 Review Phase 0 against EnterpriseBlueprints, current ThinkPixelAG contracts, current Kubernetes Agent Sandbox behavior, and Codex App Server capabilities; record gaps/deviations. — completed 2026-08-28, commit `0850b6b`, evidence: pinned revisions, official KAS tag/v1.0.0 source, Codex `0.150.1` generated schemas, gap/owner/closure ledger in `docs/evidence/phase-0-cross-system-review.md`; `git diff --check`
- [x] ARC-036 Run documentation/schema validation and commit Phase 0 with review evidence. — completed 2026-08-28, commit `d75481c`, evidence: JSON parse, pinned OpenAPI lint, local links, 27 Mermaid/fence inventory, ARC-001..036 completion, ASCII-box scan and review/gap record in `docs/evidence/phase-0-exit.md`; `git diff --check`

---

## Phase 1 — Engineering foundation

- [x] ENG-001 Initialize Go module using a supported pinned Go release and record the tested version. — completed 2026-08-31, commit `f541676`, evidence: official archive metadata/SHA-256 match; `go version` (`go1.26.7 linux/amd64`); `go mod tidy`; `go list -m -json`; `.go-version` pin check; `git diff --check`
- [x] ENG-002 Create repository package structure matching the domain/application/ports/adapters boundary in `PLAN.md`. — completed 2026-08-31, commit `b3b9b81`, evidence: pinned Go `go list ./...`; `go test ./...`; `go vet ./...`; `git diff --check`
- [ ] ENG-003 Add dependency/source/license policy and document allowed dependency classes.
- [ ] ENG-004 Implement typed configuration loading with environment/file support, validation, safe defaults, and secret redaction.
- [ ] ENG-005 Implement structured logging with canonical request/trace/Session/Execution/Attempt correlation fields and recursive secret redaction.
- [ ] ENG-006 Implement Prometheus metric registry and OpenTelemetry trace initialization with no sensitive payload recording by default.
- [ ] ENG-007 Implement shared primitives: UUIDv7 IDs, injectable UTC clock, typed errors, authenticated cursors, bounded strings/payloads, and safe enum parsing.
- [ ] ENG-008 Implement baseline HTTP server with request IDs, W3C trace extraction, panic recovery, RFC 7807 errors, limits, timeouts, graceful shutdown, `/livez`, `/readyz`, and `/metrics`.
- [ ] ENG-009 Add OpenAPI generation/validation workflow and generated-artifact drift checks.
- [ ] ENG-010 Create root Makefile as the stable local/CI command surface.
- [ ] ENG-011 Add deterministic unit/race/static/vulnerability/license/build verification targets.
- [ ] ENG-012 Add PostgreSQL development dependency and explicit migration command skeleton.
- [ ] ENG-013 Create baseline `thinkpixelar` container image using pinned build stages and hardened non-root runtime image.
- [ ] ENG-014 Create baseline `thinkpixel-agentd` container/build target separately from vendor agent images.
- [ ] ENG-015 Add CI with least-privilege jobs, immutable action pins where practical, dependency cache isolation, and full baseline verification.
- [ ] ENG-016 Add repository hygiene checks preventing local Workspace state, vendor credentials, kubeconfigs, generated sandbox credentials, binaries, and test secrets from being committed.
- [ ] ENG-017 Start `docs/supported-versions.md` with exact tested component pins obtained from reproducible environment validation.
- [ ] ENG-018 Verify clean checkout baseline: generation, format, vet/static, unit, race, vulnerability/license, OpenAPI, binary build, and image smoke.
- [ ] ENG-019 Commit Phase 1 with evidence under `docs/phase-1-evidence.md`.

---

## Phase 2 — Authoritative AR persistence and domain state

- [ ] DB-001 Add migration framework and first schema migration for tenant-scoped Sessions.
- [ ] DB-002 Add Sessions domain aggregate, transition validation, optimistic state version, timestamps, and table-driven tests.
- [ ] DB-003 Add Executions schema/domain including Session binding, authority reference, immutable resolved agent/version evidence, deadline, state, and generation.
- [ ] DB-004 Add Attempts schema/domain including current-attempt designation, lifecycle, Sandbox/Harness references, heartbeat timestamps, and terminal result.
- [ ] DB-005 Add database-enforced invariant preventing multiple active mutable Executions for one Session.
- [ ] DB-006 Add monotonic Session execution epoch/fence and tests preventing stale Attempt mutation.
- [ ] DB-007 Add Workspace and WorkspaceGeneration metadata schema/domain.
- [ ] DB-008 Add Checkpoint metadata schema/domain with immutable integrity and generation references.
- [ ] DB-009 Add Runtime Profile resolution snapshot persistence.
- [ ] DB-010 Add SandboxBinding and HarnessBinding persistence without leaking Kubernetes/vendor types into the domain.
- [ ] DB-011 Add append-only ordered Runtime Event persistence with per-stream sequencing and payload/reference bounds.
- [ ] DB-012 Add transaction manager and repository interfaces/implementations.
- [ ] DB-013 Add mutation idempotency records with concurrent ownership, request digest validation, response persistence, and expiry.
- [ ] DB-014 Add transactional outbox with claim lease, retry, replay-safe event identity, and dead-letter metadata.
- [ ] DB-015 Add reconciler work-claim/lease mechanism where process coordination cannot rely on AG leases.
- [ ] DB-016 Add cleanup/orphan metadata required for retrying incomplete Sandbox, Workspace, and checkpoint operations.
- [ ] DB-017 Add migration-from-empty tests on a real pinned PostgreSQL instance.
- [ ] DB-018 Add transaction rollback tests including forced failure after external-reference reservation but before final commit.
- [ ] DB-019 Add tenant-isolation integration tests for every repository.
- [ ] DB-020 Add concurrency tests for active Execution uniqueness, Attempt fencing, event sequence allocation, idempotency races, and reconciliation claims.
- [ ] DB-021 Add property/fuzz tests for legal Session/Execution/Attempt transitions.
- [ ] DB-022 Verify restart/replay behavior for outbox and reconciliation work.
- [ ] DB-023 Commit Phase 2 with schema/domain evidence.

---

## Phase 3 — Kubernetes Agent Sandbox substrate

- [ ] KAS-001 Implement Kubernetes client/configuration port with in-cluster and development kubeconfig modes and bounded API timeouts.
- [ ] KAS-002 Add Kubernetes Agent Sandbox API dependency at the exact Phase 0 pin without exposing its types outside the adapter.
- [ ] KAS-003 Implement Runtime Profile configuration loader and validation.
- [ ] KAS-004 Implement `KubernetesAgentSandboxProvider.Acquire`.
- [ ] KAS-005 Implement provider status translation from Kubernetes Agent Sandbox state to AR-neutral Sandbox status.
- [ ] KAS-006 Implement provider release/delete with idempotent semantics.
- [ ] KAS-007 Implement provider suspend operation using supported upstream semantics.
- [ ] KAS-008 Implement provider resume operation using supported upstream semantics.
- [ ] KAS-009 Implement SandboxTemplate mapping for the initial coding Runtime Profile.
- [ ] KAS-010 Implement SandboxClaim-based acquisition where appropriate and persist stable AR↔sandbox binding.
- [ ] KAS-011 Add Workspace attachment/materialization seam without embedding CSI types into domain objects.
- [ ] KAS-012 Configure the high-isolation profile to select Kata through operator-controlled RuntimeClass mapping.
- [ ] KAS-013 Enforce/verify no service-account token, host namespaces, hostPath, privileged mode, container-runtime sockets, or unnecessary capabilities in the secure profile.
- [ ] KAS-014 Add NetworkPolicy/egress enforcement hooks for Runtime Profile network classes.
- [ ] KAS-015 Add metadata-service and Kubernetes API denial tests for the secure coding profile.
- [ ] KAS-016 Implement AR reconciler for desired Session/Execution state against actual Sandbox state.
- [ ] KAS-017 Make sandbox acquisition/release reconciliation restart-safe and idempotent.
- [ ] KAS-018 Detect orphaned/missing Sandboxes and create explicit recovery work rather than silently marking Sessions terminal.
- [ ] KAS-019 Add disposable-cluster integration suite for acquire/readiness/release/recreate.
- [ ] KAS-020 Add suspend/resume integration tests where supported by the pinned upstream release.
- [ ] KAS-021 Run separate Kata-capable environment test proving the secure profile actually receives the intended isolation runtime.
- [ ] KAS-022 Verify resource requests/limits and ephemeral-storage boundaries are physically applied.
- [ ] KAS-023 Document Kubernetes Agent Sandbox version/API assumptions and upstream compatibility policy.
- [ ] KAS-024 Commit Phase 3 with cluster manifests and evidence.

---

## Phase 4 — `thinkpixel-agentd` and sandbox transport

- [ ] AGD-001 Define versioned AR↔agentd protocol schema and compatibility handshake.
- [ ] AGD-002 Implement `thinkpixel-agentd` process lifecycle and configuration with no Kubernetes credentials.
- [ ] AGD-003 Implement sandbox-scoped authenticated transport selected in Phase 0.
- [ ] AGD-004 Add certificate/token issuance/rotation or equivalent sandbox-scoped authentication mechanism.
- [ ] AGD-005 Ensure one sandbox cannot authenticate as another SandboxBinding.
- [ ] AGD-006 Implement bounded start/stop/restart commands for harness processes.
- [ ] AGD-007 Implement process stdout/stderr/event capture with size limits, backpressure, and redaction.
- [ ] AGD-008 Implement structured health/status messages and heartbeat.
- [ ] AGD-009 Implement signal/interrupt handling.
- [ ] AGD-010 Implement vendor durable-state path registration and checkpoint preparation hooks.
- [ ] AGD-011 Ensure execution-scoped environment/credentials are not copied into persistent vendor-state paths by agentd itself.
- [ ] AGD-012 Implement graceful SIGTERM shutdown and bounded harness termination escalation.
- [ ] AGD-013 Implement control-plane reconnect behavior after AR process restart.
- [ ] AGD-014 Add malformed/replayed/oversized message tests.
- [ ] AGD-015 Add transport-loss and half-open connection tests.
- [ ] AGD-016 Add sandbox-local privilege tests proving agentd has no unexpected host/Kubernetes capability.
- [ ] AGD-017 Add test harness adapter/process used only for deterministic lifecycle tests.
- [ ] AGD-018 Verify AR remains authoritative when agentd falsely reports impossible/obsolete Attempt state.
- [ ] AGD-019 Commit Phase 4 with protocol/security evidence.

---

## Phase 5 — Harness contract and Codex adapter

- [ ] HNS-001 Implement generic `HarnessAdapter` interface and capability model.
- [ ] HNS-002 Implement adapter registry keyed by stable adapter kind and compatibility range.
- [ ] HNS-003 Create HarnessAdapter conformance-test framework independent of Codex.
- [ ] HNS-004 Define canonical normalized harness event types and adapter mapping rules.
- [ ] CDX-001 Pin a tested Codex/App Server version and document compatibility/support policy.
- [ ] CDX-002 Build immutable Codex agent runtime OCI image containing required runtime dependencies and `thinkpixel-agentd`.
- [ ] CDX-003 Implement Codex App Server startup and protocol handshake.
- [ ] CDX-004 Implement thread creation and persist vendor thread identity in HarnessBinding/checkpoint state.
- [ ] CDX-005 Implement thread resume.
- [ ] CDX-006 Implement turn start mapped to AR Execution input.
- [ ] CDX-007 Normalize Codex streamed item/message/tool/process events into AR Runtime Events.
- [ ] CDX-008 Implement turn completion mapping and usage/reference capture without trusting it for authoritative AG accounting.
- [ ] CDX-009 Implement turn interrupt/cancellation.
- [ ] CDX-010 Implement malformed protocol/event handling and safe adapter failure.
- [ ] CDX-011 Implement App Server crash detection and recovery classification.
- [ ] CDX-012 Persist only required Codex state paths and explicitly exclude transient credentials.
- [ ] CDX-013 Implement native thread fork support behind the generic capability interface without making Codex semantics canonical.
- [ ] CDX-014 Add version incompatibility detection/fail-fast behavior.
- [ ] CDX-015 Run generic conformance suite against Codex adapter.
- [ ] CDX-016 Run Codex adapter inside Kubernetes Agent Sandbox rather than only as local process tests.
- [ ] CDX-017 Verify a malicious Codex/repository process cannot access Kubernetes service credentials or host interfaces under the secure profile.
- [ ] CDX-018 Record exact adapter compatibility and known limitations.
- [ ] CDX-019 Commit Phase 5 with Codex conformance evidence.

---

## Phase 6 — Stateful standalone Runtime MVP

- [ ] API-001 Implement OIDC/JWT verification, issuer/audience/algorithm/expiry validation, and claim-to-principal/tenant mapping.
- [ ] API-002 Implement explicitly configured development/local authentication mode that cannot accidentally activate in production configuration.
- [ ] AUT-001 Implement bounded `LocalAuthority` using operator-defined Runtime Profile, duration, resource, and network constraints.
- [ ] AUT-002 Make local authority grants immutable after issuance except for cancellation/expiry state.
- [ ] AUT-003 Clearly expose local-authority mode in telemetry/API so it cannot be mistaken for AG-governed execution.
- [ ] SES-001 Implement `POST /v1/sessions` with idempotency, agent/runtime resolution, Runtime Profile validation, and tenant ownership.
- [ ] SES-002 Implement `GET /v1/sessions` and `GET /v1/sessions/{id}` with cursor pagination and enumeration-safe authorization.
- [ ] SES-003 Implement Session close/delete semantics with active-Execution checks and idempotent cleanup.
- [ ] EXE-001 Implement `POST /v1/sessions/{id}/executions`.
- [ ] EXE-002 Bind every Execution to an immutable ExecutionGrant and resolved agent/runtime version.
- [ ] EXE-003 Implement Execution status retrieval.
- [ ] EXE-004 Implement Execution signal/input endpoint with legal-state validation.
- [ ] EXE-005 Implement Execution cancellation and races with completion/failure.
- [ ] EVT-001 Implement Session SSE stream with ordered cursor resume, heartbeat, authorization, retention, and backpressure.
- [ ] EVT-002 Implement Execution SSE stream using the same event contract.
- [ ] WSP-001 Implement first production `WorkspaceProvider` using Kubernetes CSI/PVC semantics.
- [ ] WSP-002 Implement empty Workspace creation and durable mount at the canonical Workspace path.
- [ ] WSP-003 Implement Workspace generation advancement on successful durable checkpoint boundaries.
- [ ] WSP-004 Implement snapshot/checkpoint operation for the selected storage environment.
- [ ] CHK-001 Implement Checkpoint publication only after required Workspace/vendor state is durably committed.
- [ ] CHK-002 Validate checkpoint integrity and compatibility before resume.
- [ ] SES-004 Implement Session suspend with active-Execution exclusion and durable checkpoint requirement.
- [ ] SES-005 Implement Session resume onto replacement Sandbox.
- [ ] SES-006 Restart harness with fresh execution-local environment while restoring Codex thread/vendor state.
- [ ] SES-007 Ensure old execution credentials are absent/expired after resume.
- [ ] REC-001 Recover after `thinkpixelar` process restart with no Session loss.
- [ ] REC-002 Recover after `agentd` crash according to Attempt policy.
- [ ] REC-003 Recover after harness process crash without violating Attempt/session fences.
- [ ] REC-004 Recover after Sandbox deletion by creating a replacement Attempt/Sandbox.
- [ ] REC-005 Handle node loss and Sandbox disappearance as recoverable infrastructure failure where checkpoint state permits.
- [ ] REC-006 Reconcile orphaned SandboxBinding records and abandoned physical Sandboxes safely.
- [ ] SEC-001 Enforce one mutable Execution per Session and reject/fence concurrent stale writers.
- [ ] E2E-001 Add standalone end-to-end: create Session → run Codex → checkpoint → suspend → delete Sandbox → resume → second Execution.
- [ ] E2E-002 Verify Session, Workspace, and Codex conversation continuity across complete compute replacement.
- [ ] E2E-003 Verify every resumed Execution receives fresh local authority rather than reusing previous Execution secrets.
- [ ] MVP-001 Run the full standalone Runtime MVP gate from a clean deployment.
- [ ] MVP-002 Publish `docs/mvp-standalone-evidence.md` with architecture, commands, timings, failures tested, and known limitations.
- [ ] MVP-003 Commit Phase 6 as the first usable standalone ThinkPixelAR milestone.

---

## Phase 7 — ThinkPixel integrated MVP

- [ ] TAG-001 Generate/add ThinkPixelAG client adapter without leaking AG transport types into AR domain.
- [ ] TAG-002 Implement secure AR→AG authentication and caller/delegation contract selected in Phase 0.
- [ ] TAG-003 Implement governed Execution admission through `ThinkPixelAGAuthority`.
- [ ] TAG-004 Persist AG `run_id`/authority reference and resolved immutable agent version evidence on Execution.
- [ ] TAG-005 Enforce Session bound-version consistency against every subsequent AG Run.
- [ ] TAG-006 Fail closed when Session-bound version is revoked or no longer valid for continuation.
- [ ] TAG-007 Implement AG worker claim/lease/heartbeat integration where required.
- [ ] TAG-008 Store and enforce AG fencing token/lease semantics so stale Attempts cannot report terminal Run results.
- [ ] TAG-009 Map AG cancellation/timeout/budget/revocation outcomes to AR execution actions without inventing conflicting authority.
- [ ] TAG-010 Report AR completion/failure/timeout to AG idempotently.
- [ ] TAG-011 Reconcile AR state against authoritative AG Run state after disconnect/restart.
- [ ] TAG-012 Verify AR cannot make an AG Run more permissive than its issued resource/runtime constraints.
- [ ] LLM-001 Add ThinkPixelLLMGW endpoint/runtime configuration integration.
- [ ] LLM-002 Implement issuance/injection mechanism for bounded Run/Execution-scoped LLMGW authority.
- [ ] LLM-003 Verify provider API credentials never enter the sandbox in integrated mode.
- [ ] LLM-004 Verify expired/completed/revoked Execution authority can no longer invoke LLMGW.
- [ ] TPG-001 Add ThinkPixelTG endpoint/runtime configuration integration.
- [ ] TPG-002 Implement issuance/injection mechanism for bounded Run/Execution-scoped TG authority.
- [ ] TPG-003 Verify downstream tool credentials never enter the sandbox.
- [ ] TPG-004 Verify harness-local approval cannot bypass TG/AG authorization for external side effects.
- [ ] TPG-005 Add governed Workspace Source adapter capable of materializing repository content without giving reusable SCM credentials to the sandbox.
- [ ] TPG-006 Preserve stable logical tool-call identity across adapter/process restart so ambiguous external outcomes are not blindly retried.
- [ ] GRD-001 Document GR integration path through LLMGW/TG and verify AR does not become a competing guardrail authority.
- [ ] NET-001 Implement `thinkpixel-only` network profile allowing only required gateway/control/artifact endpoints plus DNS.
- [ ] NET-002 Verify direct provider endpoints are unreachable from the secure integrated profile.
- [ ] NET-003 Verify Kubernetes API, cloud metadata, internal private ranges, and arbitrary Internet endpoints follow configured deny rules.
- [ ] SEC-002 Implement execution-secret cleanup and harness restart between completed governed Runs.
- [ ] SEC-003 Verify a compromised previous Run cannot reuse stale gateway authority during the next Run.
- [ ] E2E-004 Add integrated flow: AG admission → AR Sandbox → Codex → LLMGW → TG → completion.
- [ ] E2E-005 Add integrated flow: first Run completes → Session suspends → replacement Sandbox → second AG Run → Codex resumes.
- [ ] E2E-006 Add AG cancel during active model streaming.
- [ ] E2E-007 Add AG cancel/revoke while agent is attempting a TG operation.
- [ ] E2E-008 Verify cross-component trace correlation using tenant, Session, AG Run, Execution, Attempt, Sandbox, LLMGW request, and TG invocation identifiers.
- [ ] E2E-009 Inspect running Sandbox and checkpoint artifacts to prove absence of provider/downstream long-lived credentials.
- [ ] MVP-004 Run the complete ThinkPixel-integrated MVP gate.
- [ ] MVP-005 Publish `docs/mvp-thinkpixel-evidence.md` including the governed PR-review/suspend/resume scenario.
- [ ] MVP-006 Commit Phase 7 as the ThinkPixel-integrated MVP milestone.

---

## Phase 8 — Recovery, fork, isolation, and performance hardening

- [ ] RCV-001 Add explicit recovery decision table covering every Session/Execution/Attempt/Sandbox combination after controller restart.
- [ ] RCV-002 Add repeated crash/restart tests during Sandbox acquisition.
- [ ] RCV-003 Add crash tests during harness startup.
- [ ] RCV-004 Add crash tests during checkpoint publication.
- [ ] RCV-005 Add crash tests during suspend.
- [ ] RCV-006 Add crash tests during resume.
- [ ] RCV-007 Add cancellation-vs-completion race stress tests.
- [ ] RCV-008 Add stale Attempt takeover tests with repeated delayed messages from the old sandbox.
- [ ] RCV-009 Add PostgreSQL latency/outage/recovery scenarios.
- [ ] RCV-010 Add Kubernetes API partition/outage scenarios.
- [ ] RCV-011 Add AG outage/reconciliation scenarios.
- [ ] RCV-012 Add LLMGW/TG outage classifications and verify no unsafe blind retries.
- [ ] FRK-001 Implement Workspace snapshot clone where the selected CSI backend supports it.
- [ ] FRK-002 Implement Session fork API/domain operation with new Session identity and independent write generation.
- [ ] FRK-003 Use Codex native thread fork when safe/compatible while preserving vendor-neutral AR semantics.
- [ ] FRK-004 Verify modifications in one fork cannot modify the other Workspace.
- [ ] WRM-001 Add Kubernetes Agent Sandbox warm-pool integration after cold lifecycle correctness is proven.
- [ ] WRM-002 Define which Runtime Profiles/agent images are eligible for warm pools.
- [ ] WRM-003 Verify claimed warm Sandboxes contain no previous tenant/session Workspace, process, environment, credential, or vendor-state residue.
- [ ] WRM-004 Measure cold vs warm Sandbox acquisition and first-agent-response latency.
- [ ] WRM-005 Ensure warm-pool failure degrades to normal cold acquisition rather than violating Session correctness.
- [ ] SEC-004 Add hostile repository fixture attempting environment-secret extraction.
- [ ] SEC-005 Add hostile repository fixture attempting service-account/Kubernetes API access.
- [ ] SEC-006 Add hostile repository fixture attempting cloud metadata access.
- [ ] SEC-007 Add hostile repository fixture attempting host filesystem/container-runtime socket access.
- [ ] SEC-008 Add hostile repository fixture attempting arbitrary/private-network egress.
- [ ] SEC-009 Add hostile repository fixture instructing the model to bypass gateways and platform rules.
- [ ] SEC-010 Verify security tests succeed through infrastructure enforcement even if the harness attempts prohibited actions.
- [ ] SEC-011 Add Workspace path traversal/symlink/mount-boundary tests.
- [ ] SEC-012 Add checkpoint scanning tests for accidentally persisted execution credentials.
- [ ] SUP-001 Add immutable image-digest validation.
- [ ] SUP-002 Add pluggable image signature/provenance verification hook.
- [ ] SUP-003 Add SBOM/provenance metadata exposure for operator diagnostics.
- [ ] CAP-001 Add configured global/per-tenant Session and Sandbox capacity limits appropriate to AR standalone/runtime concerns.
- [ ] CAP-002 Add reconciler backpressure and bounded work queues.
- [ ] CAP-003 Add load tests for concurrent Session creation, Sandbox acquisition, SSE fanout, suspend/resume, and reconciliation.
- [ ] CAP-004 Document tested capacity envelope and primary scaling bottlenecks.
- [ ] HRD-001 Run full adversarial suite in a Kata-capable environment.
- [ ] HRD-002 Run full chaos/recovery suite repeatedly and archive failure/recovery evidence.
- [ ] HRD-003 Commit Phase 8 with security/performance evidence.

---

## Phase 9 — Production packaging and operations

- [ ] OPS-001 Finalize reproducible `thinkpixelar` and `thinkpixel-agentd` images with build metadata and immutable digests.
- [ ] OPS-002 Finalize reference Codex runtime image and document rebuild/update process.
- [ ] OPS-003 Create Helm chart for AR control plane, migration Job, service account, configuration, secret references, Service, and optional ingress.
- [ ] OPS-004 Add required RBAC with least privilege for only the Kubernetes Agent Sandbox and related resources AR actually needs.
- [ ] OPS-005 Add NetworkPolicies for AR control plane and reference sandbox profiles.
- [ ] OPS-006 Add hardened AR control-plane pod security context, seccomp, dropped capabilities, read-only root filesystem, and bounded writable temp.
- [ ] OPS-007 Add startup/readiness/liveness probes reflecting process, PostgreSQL, Kubernetes API, and required provider readiness without restart-looping on transient dependency failure.
- [ ] OPS-008 Add PodDisruptionBudget and topology-spread guidance.
- [ ] OPS-009 Add optional HPA based on appropriate API/reconciliation signals.
- [ ] OPS-010 Add ServiceMonitor/PodMonitor resources where applicable.
- [ ] OPS-011 Create dashboards for Sessions, Executions, Attempts, Sandbox lifecycle, start/resume latency, checkpoints, Workspace, adapter health, reconciliation, PostgreSQL, and Go runtime.
- [ ] OPS-012 Define alerts tied to SLOs with severity, ownership, and runbook references.
- [ ] OPS-013 Write install/configuration runbook including Kubernetes Agent Sandbox and Kata prerequisites.
- [ ] OPS-014 Write Runtime Profile/operator configuration guide.
- [ ] OPS-015 Write Codex adapter/version upgrade runbook.
- [ ] OPS-016 Write Session recovery/orphan Sandbox runbook.
- [ ] OPS-017 Write PostgreSQL migration/backup/restore runbook.
- [ ] OPS-018 Write AG/LLMGW/TG outage/degraded-mode runbook.
- [ ] OPS-019 Write credential/token rotation and suspected sandbox-compromise runbook.
- [ ] OPS-020 Write warm-pool drain/rebuild runbook.
- [ ] OPS-021 Test PostgreSQL backup/restore preserving Session generations, Attempt fences, checkpoints, outbox, and idempotency records.
- [ ] OPS-022 Test fresh install into disposable cluster.
- [ ] OPS-023 Test upgrade from previous schema/chart version.
- [ ] OPS-024 Test failed upgrade followed by documented rollback/roll-forward path.
- [ ] OPS-025 Test Kubernetes node disruption and AR rolling restart during active Sessions.
- [ ] OPS-026 Test uninstall behavior with explicit policies for retaining/deleting Workspaces and Session metadata.
- [ ] OPS-027 Run production-like load test against documented target capacity.
- [ ] OPS-028 Generate SBOM and vulnerability reports for all released AR and reference-agent images.
- [ ] OPS-029 Add provenance/signature hooks and release checksums.
- [ ] OPS-030 Add release automation producing images, chart package, OpenAPI artifact, checksums, SBOM/provenance, and draft release notes.
- [ ] OPS-031 Commit Phase 9 with operations evidence.

---

## Phase 10 — Release-candidate closure

- [ ] RC-001 Freeze OpenAPI, event vocabulary, Runtime Profile schema, AgentRuntimeSpec schema, HarnessAdapter compatibility contract, and checkpoint format for the RC.
- [ ] RC-002 Run backward-compatibility and generated-artifact drift checks.
- [ ] RC-003 Run `make verify` from a clean checkout.
- [ ] RC-004 Archive unit, race, fuzz/property, PostgreSQL integration, harness conformance, contract, security, Kubernetes, Kata, e2e, and chaos evidence.
- [ ] RC-005 Confirm the standalone stateful Session flow passes from a fresh deployment.
- [ ] RC-006 Confirm the complete ThinkPixel-integrated governed Session flow passes from a fresh deployment.
- [ ] RC-007 Confirm original Sandbox deletion followed by Session resume and new Execution works reliably.
- [ ] RC-008 Confirm previous Execution credentials cannot be reused by resumed/new Executions.
- [ ] RC-009 Confirm provider and downstream enterprise credentials are absent from the secure integrated sandbox, Workspace, checkpoint, and runtime event records.
- [ ] RC-010 Confirm AG stale lease/fencing attempts cannot mutate terminal/current Run state.
- [ ] RC-011 Confirm one mutable Session writer invariant under concurrency/stale Attempt stress.
- [ ] RC-012 Confirm all mandatory secure Runtime Profile isolation tests pass on real Kata-capable infrastructure.
- [ ] RC-013 Confirm no unresolved critical/high vulnerabilities or security findings.
- [ ] RC-014 Confirm no required test is skipped/flaky without explicit documented disposition.
- [ ] RC-015 Confirm no undocumented fail-open path exists for identity, authority, sandbox isolation, network policy, credential handling, or recovery.
- [ ] RC-016 Confirm SLO/capacity targets and document tested scaling envelope.
- [ ] RC-017 Exercise and record install, upgrade, rollback/forward, backup/restore, node disruption, AR rolling restart, AG outage, and Sandbox-loss game days.
- [ ] RC-018 Reconcile every TODO item against implementation, tests, commits, docs, and release artifacts.
- [ ] RC-019 Update README with architecture, positioning, quick start, standalone mode, ThinkPixel-integrated mode, API, runtime profiles, Codex support, deployment, security, and known limitations.
- [ ] RC-020 Create numbered ADRs for all durable decisions listed in `PLAN.md`.
- [ ] RC-021 Verify ADRs preserve meaningful rejected alternatives and implementation lessons rather than only recording final choices.
- [ ] RC-022 Prepare RC release notes, support/version matrix, known limitations, operator checklist, and artifact inventory.
- [ ] RC-023 Explicitly document post-RC scope: Temporal reconsideration, Execution Cells, Claude/Copilot, ACP/A2A, generic PTY, GPU profiles, subagents, and agent-version migration.
- [ ] RC-024 Remove `PLAN.md` and `TODO.md` only after all durable rationale has been transferred into ADRs/permanent docs.
- [ ] RC-025 Run documentation/link validation and `make verify` against the resulting tree.
- [ ] RC-026 Commit final documentation transition to `main`.
- [ ] RC-027 Build all release artifacts from that exact commit and verify image/chart/checksum/SBOM/provenance consistency.
- [ ] RC-028 Create/tag the release candidate only after all previous gates pass.

---

## Deferred / post-RC backlog

These items are recorded but intentionally do not block the first RC.

- [ ] FUT-001 Evaluate Temporal or another durable workflow engine only against demonstrated orchestration requirements that reconciliation cannot cleanly satisfy.
- [ ] FUT-002 Define Execution Cell abstraction for multi-cluster scheduling.
- [ ] FUT-003 Implement a second SandboxProvider only if a real alternative backend justifies it.
- [ ] FUT-004 Add Claude harness adapter and run full HarnessAdapter conformance suite.
- [ ] FUT-005 Add GitHub Copilot harness adapter and run full conformance suite.
- [ ] FUT-006 Add generic structured-CLI adapter.
- [ ] FUT-007 Add PTY compatibility adapter for harnesses with no structured protocol.
- [ ] FUT-008 Add ACP northbound compatibility adapter.
- [ ] FUT-009 Add A2A northbound compatibility adapter.
- [ ] FUT-010 Add automatic Session migration between compatible agent versions.
- [ ] FUT-011 Add GPU Runtime Profiles and scheduling semantics.
- [ ] FUT-012 Add governed child/subagent execution semantics integrated with AG child resource reservations.
- [ ] FUT-013 Add cross-cluster Session migration using portable Workspace/checkpoint storage.
- [ ] FUT-014 Add confidential-compute Runtime Profiles if an implementation provides meaningful additional guarantees.
- [ ] FUT-015 Evaluate memory/context integration ports without moving cognitive memory ownership into AR.
- [ ] FUT-016 Add web administration/diagnostic interface only after operational APIs and CLI workflows are stable.

---

## Progress log

Append one entry per completed atomic item or tightly coupled group.

Do not delete historical entries. Supersede incorrect or obsolete assumptions with a later entry.

Date | TODO IDs | Commit | Verification evidence | Notes/deviations
--- | --- | --- | --- | ---
YYYY-MM-DD | `ARC-...` | `<sha>` | `<commands/artifacts>` | `<notes>`
2026-08-28 | `ARC-001` | `17cef58` | `find docs -type f`; ADR required-section validation | Established the durable documentation structure and ADR lifecycle.
2026-08-28 | `ARC-002` | `473fbf8` | required-component coverage; Mermaid fence validation; `git diff --check` | Defines integrated and standalone boundaries; no implementation-specific CRD enters the public domain.
2026-08-28 | `ARC-003` | `f1a7b59` | compromise-assumption and TM-01..TM-16 coverage; `git diff --check` | Treats every sandbox-originated claim as untrusted until externally validated.
2026-08-28 | `ARC-004` | `93a660c` | required-category matrix; prohibited-content and redaction-rule validation; `git diff --check` | Unknown data defaults to Restricted; hidden chain-of-thought is explicitly excluded.
2026-08-28 | `ARC-005` | `cafcdd1` | all ten canonical definitions; identity/lifetime matrix; Mermaid fence validation; `git diff --check` | Prohibits generic “run” from collapsing distinct identities.
2026-08-28 | `ARC-006` | `d45a3d7` | eight-state coverage; transition and illegal-edge rules; Mermaid fence validation; `git diff --check` | Recovery is durable work while Session remains DEGRADED, not a process-oriented transient state.
2026-08-28 | `ARC-007` | `82bf7ee` | Execution/Attempt state coverage; cancel/timeout/crash/retry/replacement/race rules; Mermaid fence validation; `git diff --check` | PostgreSQL serialization selects one terminal winner; ambiguous external effects are never blindly retried.
2026-08-28 | `ARC-008` | `d8822bd` | database invariant, monotonic-generation, mutation-predicate, replacement, and AG-composition coverage; `git diff --check` | Reconciler leases coordinate work but never replace aggregate fencing.
2026-08-28 | `ARC-009` | `e98a858` | RunAuthority method semantics; LocalAuthority configuration/admission/lifecycle/non-guarantee/conformance coverage; `git diff --check` | Local mode is explicit, bounded, finite, non-renewing, and never a fallback for failed AG integration.
2026-08-28 | `ARC-010` | `687bc2d` | AG revision and source review; operation mapping; AG-API-001..008 gap coverage; `git diff --check` | Current worker API is internal only; targeted externally callable worker operations and stable mutation identity are blocking cross-project changes.
2026-08-28 | `ARC-011` | `510ef17` | identity-source, OBO exchange, credential-path separation, confused-deputy, audit, and failure coverage; `git diff --check` | Caller/OBO and AR worker credentials are separate; JSON and forwarding headers convey no identity authority.
2026-08-28 | `ARC-012` | `4f6dc89` | accepted ADR; immutable binding, fresh continuation authorization, deprecation/revocation, and migration coverage; `git diff --check` | Future version migration creates a new Session with lineage; it never mutates the source binding.
2026-08-28 | `ARC-013` | `be3861a` | JSON parse and required-field checks; digest/adapter/entrypoint/path/mount/profile/platform/canonicalization coverage; `git diff --check` | Machine-readable closed v1 schema plus cross-field validation contract.
2026-08-28 | `ARC-014` | `180b23d` | interface, neutral type/state, operation, error, persistence, security, and conformance coverage; `git diff --check` | Kubernetes types and lifecycle details remain wholly inside the production adapter.
2026-08-28 | `ARC-015` | `83fe4f3` | upstream release review; exact baseline plus Agent Sandbox, Kata/containerd, CSI, skew/update, and qualification matrices; `git diff --check` | Pins qualification candidates honestly; release-qualified support awaits later reproducible cluster/Kata evidence.
2026-08-28 | `ARC-016` | `54e4e5a` | JSON parse/section checks; isolation class, resolution, secure invariant, and concrete profile validation; `git diff --check` | Initial secure coding profile is microvm-strong, Kata-mapped, bounded, default-deny, credentialless, snapshot-backed, and not warm-pool eligible.
2026-08-28 | `ARC-017` | `f33d8dc` | interface, registered capabilities, four-way negotiation, lifecycle/event/failure, and conformance coverage; `git diff --check` | Codex must pass the vendor-neutral suite; sandbox adapter observations never become authority.
2026-08-28 | `ARC-018` | `d14c2df` | trust, one-Sandbox process model, bootstrap, responsibilities, non-responsibilities, credentials, failure/reconnect, and verification coverage; `git diff --check` | Initial unexplained agentd crash policy fences/replaces the Attempt/Sandbox; concrete transport remains ARC-019.
2026-08-28 | `ARC-019` | `0cac5f3` | accepted transport ADR; pinned Agent Sandbox capability/alternative review; mTLS identity, bootstrap/rotation, envelope/replay, bounds, network, failure, and verification coverage | Selects outbound agentd-initiated gRPC/TLS 1.3; no inbound Sandbox Service/router/port-forward required.
2026-08-28 | `ARC-020` | `b8565f1` | aggregate/generation, neutral provider, attach/detach, snapshot/publication, clone, deletion, failure, and security coverage; `git diff --check` | Generations advance only at committed durable boundaries; provider objects never substitute for AR identity/integrity.
2026-08-28 | `ARC-021` | `e08054b` | source union/provider, governed resolution, staging/publication, filesystem safety, provenance, failure, and conformance coverage; `git diff --check` | Secure integrated mode materializes through trusted adapters/TG and never passes repository credentials into the sandbox.
2026-08-28 | `ARC-022` | `c9f2bd4` | format/schema, Workspace/vendor linkage, canonical signed integrity, compatibility, atomic publication, credential exclusions, lifecycle, and failure coverage; `jq empty`; `git diff --check` | A committed Checkpoint is immutable durable state and never contains or conveys authority.
2026-08-28 | `ARC-023` | `26f0929` | durable release gate, suspend/resume sequencing, fresh restore, concurrency, recovery, and security coverage; `git diff --check` | Compute release follows the authoritative SUSPENDED/checkpoint transaction; cleanup may lag but stale compute is fenced.
2026-08-28 | `ARC-024` | `72a7282` | accepted fork ADR; combined qualification, identity/lineage, protocol, isolation, alternatives, failure, and verification coverage; `git diff --check` | Fork API seam is retained but RC support is gated on qualified storage plus adapter behavior.
2026-08-28 | `ARC-025` | `d4c2fd2` | five modes/schema, control/data split, selectors/DNS, layered enforcement, baseline denies, drift/failure, and verification coverage; `jq empty`; `git diff --check` | Secure enforcement requires qualified CNI/egress controls; NetworkPolicy alone cannot prove FQDN/identity semantics.
2026-08-28 | `ARC-026` | `7712e6f` | exact binding, 15-minute maximum TTL, separate audiences, injection/rotation/revocation, durable exclusion, process replacement, and verification coverage; `git diff --check` | Sandbox credentials are non-refreshable and contain no provider/downstream authority; uncertain cleanup forces Sandbox replacement.
2026-08-28 | `ARC-027` | `c3b43b6` | classification, local permission boundary, TG authorization/action binding, lifecycle/ambiguity, standalone labeling, failure, and verification coverage; `git diff --check` | Local approval cannot mint enterprise authority; TG owns downstream credentials and authoritative side-effect state.
2026-08-28 | `ARC-028` | `d18b869` | event envelope/schema, registry, transactional Session sequence, SSE replay/retention, classification/redaction, and chain-of-thought exclusion; `jq empty`; `git diff --check` | Events are immutable observations, not authority; delivery is ordered per Session and at least once.
2026-08-28 | `ARC-029` | `0a6c909` | scoped key/digest/record, seven required operations, concurrent replay/conflict, external ambiguity, retention, security, and verification coverage; `git diff --check` | Idempotency elects one logical intent; fencing still decides who may mutate and ambiguous effects are never blindly duplicated.
2026-08-28 | `ARC-030` | `cff59b4` | tenant-qualified logical schema/constraints, optimistic transactions, uniqueness, append-only events, outbox, retention/recovery, migrations, and verification coverage; `git diff --check` | PostgreSQL is authoritative; leases coordinate reconcilers but aggregate versions/fences decide mutation rights.
2026-08-28 | `ARC-031` | `fda4248` | OpenAPI 3.1 Session/Execution/lifecycle/events/capabilities/operations/health draft; version/path/operation/reference checks; `git diff --check` | Public types are vendor/Kubernetes neutral; fork is explicitly capability-gated. Parser tooling is added at the Phase 1 OpenAPI validation gate.
2026-08-28 | `ARC-032` | `be4fabc` | authentication/authorization, authenticated cursors, RFC 7807 taxonomy, request/trace correlation, limits, optimistic/idempotency headers, and SSE coverage; `git diff --check` | SSE never silently skips retention gaps or buffers without bound; identity never comes from JSON/forwarding headers.
2026-08-28 | `ARC-033` | `17b0f85` | accepted no-workflow-engine ADR; PostgreSQL reconciliation, authority boundaries, quantified reconsideration triggers, alternatives, security, and verification; `git diff --check` | Temporal is excluded from MVP/RC without foreclosing evidence-driven adoption under a superseding ADR.
2026-08-28 | `ARC-034` | `410dfc2` | SLI boundaries, API/event/reconciliation/lifecycle targets, 100-active/150-burst envelope, error budgets, RTO/RPO and load evidence requirements; `git diff --check` | Values are qualification targets and planning assumptions, not unmeasured production claims.
2026-08-28 | `ARC-035` | `0850b6b` | EnterpriseBlueprints `9c01d44`, AG `b167868`, official KAS v1.0.0 source, Codex 0.150.1 schemas, internal consistency and gap ledger; `git diff --check` | KAS v0.5.5 is superseded; AG worker API, Codex state, independent evidence and executable API/SLO validation remain explicit gates.
2026-08-28 | `ARC-036` | `d75481c` | all JSON parse; Redocly 2.3.0 valid with 19 explicit-response warnings; local links; 27 Mermaid/fences; ARC-001..036; ASCII-box and diff checks | Phase 0 contract gate complete; implementation, current substrate qualification and measured SLO evidence remain later gates.
