# Phase 1 engineering foundation evidence

- Date: 2026-09-01
- Scope: `ENG-001` through `ENG-019`
- Implementation baseline before this evidence commit: `1c6dbd1`
- Result: Phase 1 engineering-foundation gate passed with the explicitly
  deferred implementation and qualification work below.

## Delivered foundation

| Area | Evidence-bearing implementation commits |
| --- | --- |
| Go module, package boundaries, dependency policy, and typed configuration | `f541676`, `b3b9b81`, `e95946d`, `04c2784` |
| Secret-safe logging, metrics/tracing, and shared primitives | `805b761`, `8879a2a`, `f5ba14d` |
| HTTP server, health/metrics endpoints, and OpenAPI validation/drift detection | `0672d90`, `b7e2f20` |
| Stable developer commands and deterministic source verification | `d37bbb4`, `b31dce1` |
| PostgreSQL development service and explicit migration-command skeleton | `ab26549` |
| Separate hardened service and `thinkpixel-agentd` images | `5abd47b`, `e704c6f` |
| Least-privilege CI and committed-content hygiene policy | `83c1dd1`, `1c3f0bc` |
| Exact tested-version validation and clean-checkout baseline gate | `7ebcb2d`, `9ebb64b` |

The package layout continues to preserve the domain/application/ports/adapters
boundary. The images use separate entrypoints and do not merge the service,
sandbox-provider, or harness responsibilities. Development PostgreSQL is an
explicit local dependency, not evidence of the authoritative persistence work
scheduled for Phase 2.

## Exit-gate evidence

The stable root command is `make baseline-verify`. Its source portion checks
repository hygiene, exact version pins, formatting and generated-artifact
drift, vet/Staticcheck, unit and race tests, vulnerability and license policy,
all three binary builds, and OpenAPI lint/generation. Its two image portions
build the service and `thinkpixel-agentd` images and probe them with a numeric
non-root user, read-only filesystem, dropped capabilities, and
`no-new-privileges`.

ENG-018 ran every prerequisite against the `9ebb64b` source snapshot. The
source prerequisites passed across isolated scratch invocations because the
Linux `/tmp` filesystem could not hold both the hygiene fixture and Go build
scratch. Both image smoke targets ran against Docker and passed. The subsequent
`1c6dbd1` commit changes only `TODO.md` and `PLAN.md`, so the reviewed
implementation baseline is identical. Detailed command scope and limitations
are recorded in [the ENG-018 evidence](evidence/eng-018-clean-checkout-baseline.md).

Additional durable evidence:

- [supported versions and exact pins](supported-versions.md);
- [PostgreSQL development smoke](evidence/eng-012-postgresql.md);
- [service image smoke](evidence/eng-013-container-image.md);
- [`thinkpixel-agentd` image smoke](evidence/eng-014-agentd-image.md); and
- [least-privilege CI review](evidence/eng-015-ci.md).

## Deferred work and claim boundary

Phase 1 proves the repository engineering baseline and hardened build-image
baseline. It does not claim production readiness or completion of later
phases. In particular, it does not qualify:

- authoritative PostgreSQL persistence, migrations, reconciliation, or
  recovery;
- authenticated public Session/Execution behavior beyond the current baseline
  server and versioned OpenAPI contract;
- Kubernetes Agent Sandbox, Kata, CNI/egress, or a selected CSI driver;
- Workspace/checkpoint lifecycle, harness or ThinkPixel integration
  conformance, measured SLOs, or release operations; or
- the candidate cluster tuple in `docs/supported-versions.md` as
  release-qualified.

Those claims remain owned by their later TODO phases and evidence gates.

## Commit protocol

This evidence is committed before ENG-019 tracking metadata so `TODO.md` and
`PLAN.md` can record its exact immutable commit hash. The follow-up commit is
metadata-only and does not change the Phase 1 implementation baseline.
