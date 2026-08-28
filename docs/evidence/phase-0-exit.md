# Phase 0 exit evidence

- Date: 2026-08-28
- Scope: `ARC-001` through `ARC-036`
- Review baseline before this evidence commit: `0850b6b`
- Result: Phase 0 documentation/schema gate passed with the explicitly deferred implementation/qualification gaps below.

## Delivered contract set

Phase 0 now defines system/trust boundaries; threat and data handling; Session/Execution/Attempt/Workspace/Checkpoint identity and lifecycle; single-writer/fencing; standalone and AG authority; immutable runtime binding; AgentRuntimeSpec, RuntimeProfile, SandboxProvider, HarnessAdapter, agentd and transport; Workspace/storage/source/checkpoint/suspend/resume/fork; network and Execution credentials; local versus enterprise authorization; RuntimeEvents; idempotency; PostgreSQL logical persistence/outbox/migrations; public OpenAPI and HTTP/SSE conventions; workflow-engine decision; SLO/capacity targets; supported-version policy; and cross-system gap review.

The exit criterion “no ambiguous source of authority, identity, Session continuity, sandbox ownership, or mutable Workspace ownership” is satisfied at contract level:

- caller/AG/LocalAuthority issue authority; AR never derives it from sandbox or JSON identity;
- AG owns governed Run, AR owns Session/Execution runtime state, TG owns enterprise effects/credentials, LLMGW owns model-provider access;
- Session/Workspace/Checkpoint are durable; Sandbox/Attempt/process are replaceable and fenced;
- PostgreSQL aggregate versions/generations decide current mutation rights; leases only coordinate;
- one tenant Session owns one Workspace and at most one active mutable writer/Execution;
- credentials and hidden chain-of-thought are excluded from durable state/events/evidence.

## Validation performed

| Check | Result |
| --- | --- |
| All repository JSON schemas/examples parsed with `jq empty` | Pass |
| OpenAPI 3.1 parsed/linted with pinned `@redocly/cli@2.3.0` | Pass with 19 advisory `operation-4xx-response` warnings |
| Required API paths and unique operation IDs | Pass (17 required paths; 19 unique operations) |
| Repository-local Markdown links | Pass |
| Markdown code-fence balance and Mermaid inventory | Pass (27 Mermaid diagrams at review) |
| ARC-001 through ARC-036 checked and evidence-bearing | Pass after this item update; exact ARC-028..036 hashes recorded by the metadata commit |
| Residual ASCII box-pattern scan in README/PLAN/TODO/docs | Pass |
| `git diff --check` | Pass |
| Cross-system revisions/gaps | Pass; see [cross-system review](phase-0-cross-system-review.md) |

OpenAPI lint initially found missing summaries/license; this item corrected them. The remaining warnings arise because the draft uses a reusable `default` RFC 7807 response instead of explicit operation-specific 4xx entries. The document is valid; `PH0-API-002` requires explicit response matrices and negative contract tests at ENG-009. No ignore file was generated.

Representative commands:

```text
find docs -type f -name '*.json' -print0 | xargs -0 -n1 jq empty
npm_config_cache=/tmp/thinkpixelar-npm-cache npx --yes @redocly/cli@2.3.0 lint docs/api/openapi.yaml --format stylish
python3 <read-only local Markdown link checker>
python3 <read-only Markdown fence and Mermaid counter>
for n in $(seq -w 1 36); do ... checked ARC item ...; done
rg <residual ASCII-box, security term, and version-state checks>
git diff --check
```

The disposable npm cache and generated Codex schemas were under `/tmp` and are not repository artifacts.

## Review findings carried forward

Phase 0 completion is not a production-readiness claim. These gates remain explicit:

- `PH0-KAS-001`: current Agent Sandbox v1.0.0/Kubernetes/Kata/CSI selection and real qualification;
- `AG-API-001..008`: externally callable authenticated ThinkPixelAG worker contract;
- `PH0-CODEX-001..005`: Codex protocol/schema pin, mapping, reasoning filtering, approval and durable-state conformance;
- `PH0-EVIDENCE-001`: independent/precommit evidence integration without moving evidence ownership into AR;
- `PH0-API-001..002`: reproducible OpenAPI generation/validation and explicit response matrices;
- `PH0-EVENT-001`: closed per-event payload schemas and malicious/golden adapter fixtures;
- `PH0-SLO-001`: measured representative load/chaos evidence;
- CSI driver selection and every implementation/cluster/recovery/security gate in subsequent phases.

The v0.5.5 Agent Sandbox baseline is marked `SUPERSEDED_CANDIDATE`; it is not advertised as supported. SLO numbers remain targets. No application, database migration, container or cluster behavior exists yet merely because its contract is complete.

## Commit protocol

ARC-028 through ARC-036 each receive a dedicated implementation/evidence commit. Because a commit cannot contain its own final hash, TODO entries initially use a pending marker. One final metadata-only commit replaces those markers with exact immutable hashes; it changes only `TODO.md`, matching the established repository protocol.
