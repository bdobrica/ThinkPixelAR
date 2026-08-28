# Phase 0 cross-system review

- Date: 2026-08-28
- ThinkPixelAR baseline: `410dfc2ec95051bb4c953d4786801b225d0350da`
- EnterpriseBlueprints: `9c01d4462dfd9132b7399b33446365e7ffe64688`
- ThinkPixelAG: `b1678683058845b63e9188b5b60a5b01b1fcadf2`
- Agent Sandbox: tag `v1.0.0` (`317ccfdc84eec781eca3fcc45e699c54b8607d5e`), source `bb72f49d79f009a960eed2ae6c32e1cc082399c5`
- Codex: installed official `codex-cli 0.150.1`; App Server JSON schemas generated locally

This compares contracts/source available on the review date. It is not runtime conformance; “aligned” does not mean implemented.

## Result summary

| Area | Result | Release impact |
| --- | --- | --- |
| Enterprise security/separation | Substantially aligned | Preserve external authority/credentials, current authorization, side-effect ambiguity, isolation and evidence seams. |
| Enterprise workflow recommendation | Deliberate deviation | ADR-0004 permits bounded PostgreSQL reconciliation for MVP/RC with explicit reconsideration triggers. |
| ThinkPixelAG | Model aligned; external worker gap remains | Integrated RC blocked until authenticated worker claim/heartbeat/fenced transitions are callable and tested. |
| Agent Sandbox | Prior pin behind current `v1.0.0` | Requalify current line; never silently describe v0.5.5 as current/supported. |
| Codex App Server | Structured lifecycle confirmed; qualification needed | Pin schema/protocol, filter reasoning, bound approvals, prove durable state. |
| Internal Phase 0 | Core boundaries align; executable evidence remains | OpenAPI/event schemas/migrations and real-environment tests are later gates. |

## EnterpriseBlueprints

Reviewed separation, threats, identity/delegation, durable execution, side-effect ledger, isolation, evidence and failure semantics.

Aligned:

- harness/sandbox cannot self-grant authority; enterprise/provider credentials remain outside;
- user/agent/workload identity and tenant context are verified and distinct;
- authorization is checked at external effect time and revocation blocks new effects;
- ambiguous effects use stable identity/reconciliation, never blind retry;
- isolation is provider-abstracted and high-risk code receives VM-class isolation;
- telemetry is not automatically independently governed audit evidence.

Deviations/boundaries:

- The blueprint recommends Kubernetes plus Temporal/equivalent. ADR-0004 deliberately uses closed PostgreSQL reconcilers for MVP/RC while AG owns governed Runs. Its rule that workflows do not own authority is preserved; measured DAG/timer/fan-out/compensation/incident triggers force review.
- The blueprint defines a side-effect ledger and independent evidence plane. AR does not absorb them: TG owns authoritative tool-effect state and an external evidence service owns independently governed evidence. AR stores runtime idempotency/events/outbox and references. The integrated precommit-evidence handshake remains a gap.
- Child-agent/resource delegation remains explicit future scope.

## ThinkPixelAG

The revision matches ARC-010. Public OpenAPI provides caller Run create/get/list, signal, cancel and events. Application/domain/port code defines tenant-scoped worker claim, heartbeat and fenced start/complete/fail/timeout with leases/resources. That worker surface remains internal, not an authenticated external AR API.

ARC-010 `AG-API-001..008` remain open. Closure requires a versioned worker API with audience/scopes, caller/OBO read where required, stable mutation identity, lease/fence/revocation/resource evidence, normalized errors, response-loss reconciliation and cross-project conformance. AR cannot infer authority from a Run ID or import AG domain/transport types.

## Kubernetes Agent Sandbox

Official tag enumeration found `v0.5.6` and `v1.0.0` after `v0.5.5`; v1.0.0 is the latest stable tag observed. Its source retains core/extensions `v1beta1`, removes `v1alpha1`, and requires stored-version/CRD migration from v0.5.x.

- `operatingMode: Running|Suspended` is desired state; readiness/suspension conditions are observations. Suspend removes Pod while retaining volumes/Sandbox.
- Service creation is explicit; unset does not create new Services. AR's outbound transport should explicitly disable/verify inbound Service.
- template-managed NetworkPolicy's default allows public Internet while blocking internal/metadata. This is broader than AR `none`/`thinkpixel-only`, so AR must supply qualified custom policy.
- claim/template paths default service-account automount false when unspecified, but effective Pod evidence remains mandatory.
- expiry/shutdown/observed generation aid reconciliation but never replace AR lifecycle/fences.

`PH0-KAS-001` blocks qualification: choose v1.0.0 (preferred current candidate) or justify another maintained line; update Kubernetes/Kata/containerd/CSI compatibility; run migration/clean-install, lifecycle, network, storage and security conformance. ARC-019's outbound design remains structurally useful but must be rerun against the chosen version.

## Codex App Server

Official OpenAI documentation search/open was attempted but returned no retrievable App Server page in this environment. The evidence is therefore the installed official CLI's generated schema; undocumented stability is not inferred.

Schema `0.150.1` confirms `thread/start`, `thread/resume`, `thread/fork`, `turn/start`, `turn/interrupt`; thread/turn/item notifications; command/file/network approval callbacks; and assistant/command/file/tool streaming. This supports structured HarnessAdapter rather than PTY scraping.

- `PH0-CODEX-001`: pin tested CLI/build and generated schema digest; negotiate/reject unknown required or unstable protocol.
- `PH0-CODEX-002`: map thread/turn/item to Session/Execution/Attempt without authority; test reconnect/resume/interrupt/races/process replacement.
- `PH0-CODEX-003`: reasoning summary/text notifications exist, some experimental. Drop hidden reasoning/text and admit only explicitly user-visible summaries under the event registry.
- `PH0-CODEX-004`: Codex approvals are local permission, never TG enterprise authorization; session approval cache cannot outlive AR's Execution process boundary.
- `PH0-CODEX-005`: prove credential-free export/restore before advertising checkpoint/resume/fork; App Server thread storage alone is not AR integrity.

## Gap ledger

| Gap | Due gate | Closure evidence |
| --- | --- | --- |
| `PH0-KAS-001` current qualification | Phase 3 / RC | Pin/digests, v1 migration/clean tests, Kata/network/storage/lifecycle conformance. |
| `AG-API-001..008` worker authority | Integrated Phase 7 | External authenticated API and cross-project conformance. |
| `PH0-CODEX-001..005` adapter/state | Phase 4 | Schema digest, mapping/filter/approval/recovery/checkpoint tests. |
| `PH0-EVIDENCE-001` independent evidence | Integrated Phase 7 | Port/policy mode and outage/fail-closed tests; AR stores references only. |
| `PH0-API-001` executable OpenAPI | ENG-009 | Pinned parser/linter, full refs, contract/generated drift tests. |
| `PH0-EVENT-001` typed payload schemas | DB/API implementation | Registry schemas and adapter golden/malicious fixtures. |
| `PH0-SLO-001` unmeasured targets | RC qualification | Representative load/chaos evidence and accepted deviations. |

No gap permits weakening tenancy, single writer, fresh authority, credential exclusion, provider neutrality, external tool authorization or chain-of-thought exclusion.

## Evidence commands and sources

Used revision/status inspection; targeted source/OpenAPI search; official Agent Sandbox tag enumeration and shallow v1.0.0 checkout; `codex --version`; `codex app-server generate-json-schema`; generated-method inspection; diff/link/fence checks.

Sources: local pinned EnterpriseBlueprints and ThinkPixelAG OpenAPI/source; [Agent Sandbox releases](https://github.com/kubernetes-sigs/agent-sandbox/releases) and v1.0.0 source/API; locally generated schemas from official Codex CLI 0.150.1. Official OpenAI App Server documentation must be rechecked when implementing the adapter.
