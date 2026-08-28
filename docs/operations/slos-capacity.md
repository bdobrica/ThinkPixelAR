# SLOs and initial capacity assumptions

Status: Initial release-candidate targets to validate in representative Kubernetes/Kata/storage/provider environments; not measured production claims.

## Measurement rules

Latency uses server-side monotonic duration and is segmented by deployment/profile/provider/adapter/result. Availability includes valid in-policy requests and controlled failover; invalid/unauthorized/rate-limited requests and client disconnects are excluded. Dependency outages are reported separately but remain in end-to-end indicators unless contractually declared unavailable.

Percentiles use rolling 28 days with counts/max for low-volume paths. No tenant, Session, prompt, path, content, error text, or credential becomes a metric label. SLOs never override deadlines or fail-closed security.

## Service objectives

| Capability / SLI | 28-day objective | Latency target |
| --- | --- | --- |
| Public API reads/accepted mutations | 99.9% valid requests avoid `5xx`/timeout | p95 ≤250 ms, p99 ≤1 s excluding asynchronous work |
| Health/readiness | 99.95% in-cluster probes | p99 ≤100 ms |
| Durable event append | 99.95% without loss | p99 ≤250 ms added transaction latency |
| SSE establishment/catch-up start | 99.9% valid connections | p95 ≤1 s; first available event p95 ≤2 s |
| Live event visibility | 99.9% durable events visible to healthy reader | p95 ≤1 s, p99 ≤5 s from commit |
| Reconciliation freshness | 99.9% eligible due operations claimed | p95 ≤5 s, p99 ≤30 s; oldest due alert at 5 min |

At 99.9%, the 28-day API error budget is about 40.3 minutes; at 99.95%, 20.2 minutes. Multi-window burn rates alert rather than a single slow operation. Planned maintenance counts unless reporting explicitly excludes it.

## Lifecycle objectives

Measured from accepted/durable intent to ready/terminal state and segmented by strategy. Policy/capability denial is correctness, not latency failure; a valid timeout is failure.

| Operation | Boundary | Target |
| --- | --- | --- |
| Sandbox acquisition | provider intent committed → Sandbox/agentd authenticated healthy | p50 ≤15 s, p95 ≤45 s, p99 ≤90 s |
| Cold start | Execution accepted → harness ready, Workspace/source ready | p50 ≤25 s, p95 ≤60 s, p99 ≤120 s |
| Warm start | accepted → fresh harness ready in eligible existing Sandbox | p50 ≤3 s, p95 ≤10 s, p99 ≤20 s |
| Resume | accepted → restored Session `IDLE`/ready | p50 ≤20 s, p95 ≤60 s, p99 ≤120 s |
| Checkpoint | quiesce begins → committed Checkpoint | p50 ≤10 s, p95 ≤45 s, p99 ≤120 s for ≤10 GiB Workspace/256 MiB vendor state |
| Source materialization | immutable source resolved → generation 0 ready | p95 ≤120 s for ≤1 GiB/100k files; report by kind/size |

Warm start is advertised only for proven eligible profiles; the initial secure microVM profile is not eligible. Queue/admission delay is separate and included in user end-to-end start. Checkpoint and resume each target 99.5% valid operations within deadline. Integrity failures correctly fail closed but remain visible quality indicators.

## Initial capacity envelope

One RC control-plane cell is planned/tested for:

| Resource | Assumption |
| --- | --- |
| Tenants / Sessions | 100 tenants; 10,000 total Sessions, 2,000 non-closed |
| Active Executions/Sandboxes | 100 steady, 150 burst for 10 min; tenant default 10 |
| API | mutations 25/s steady, 100/s burst; reads 250/s steady, 500/s burst |
| Sandbox acquisition | 5/s burst, 100 provisioning concurrently |
| Runtime events | 2,000/s steady, 10,000/s for 5 min; 2 KiB average, 64 KiB maximum |
| SSE | 2,000 connections; 10/principal, 200/tenant; 4 MiB/1,000-event connection queue |
| Durable operations | 20 checkpoints, 20 resumes, 10 materializations concurrently per cell |
| PostgreSQL | 3 HA nodes; 500 GiB usable; pool ≤200/cell; ≥30% storage/IOPS headroom |
| Retention | terminal/state/audit references 90 days; deltas 7 days after durable completion, policy permitting |
| Outbox/reconciliation | normal backlog <10,000 and oldest due <30 s; buffer one-hour dependency outage |

These are admission/load-shedding inputs, not unlimited tenancy promises. Tenant quotas protect Sandbox, SSE, event, storage, checkpoint and database capacity. Rejection uses bounded `429/503` and creates no untracked work.

## Resource and recovery budgets

API/reconciler is horizontally scalable with at least three production replicas across failure domains. PostgreSQL steady CPU/IOPS target <60%, connections/storage <70%, replication lag p99 <5 s. Provider/Kubernetes client limits, queue concurrency, retry budgets and circuit breakers prevent storms.

Initial control-plane recovery targets are RTO ≤30 minutes and PostgreSQL RPO ≤5 minutes, subject to deployment backup configuration. Committed-state loss is never masked. Regional/multi-cluster disaster recovery is outside RC and stated operationally.

## Error budget and qualification

Good/total counters and burn-rate dashboards back each SLO. Exhaustion freezes reliability-reducing rollout and prioritizes correction; it never weakens authentication, isolation, integrity, fencing or credentials. Lifecycle histograms publish count/success/deadline; averages are insufficient.

Qualification covers steady/burst load, dependency slowdown/outage, node/replica/database failover, provider throttling, slow SSE readers, maximum events, and noisy tenants for the assumed windows. Evidence records environment pins, workload distribution, commands/raw result references, percentiles/counts/errors, bottlenecks, deviations and accepted risks.

Review when traffic/size mix changes 25%, provider/runtime/storage changes, a profile/adapter/source is added, retention changes, SLO burns, or incidents reveal a missing SLI. Until measured, values remain labelled targets.
