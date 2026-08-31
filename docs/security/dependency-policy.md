# Dependency, source, and license policy

Status: Normative engineering policy.

## Purpose

This policy governs source code, Go modules, build tools, generated clients,
container base images, packaged runtime software, and transitive dependencies
used to build, test, or operate ThinkPixelAR. It reduces supply-chain and
licensing risk without making third-party code an authority boundary.

Dependency approval does not make code trusted. In particular, dependencies
inside an Agent Sandbox remain within the compromised sandbox assumption in
the [primary threat model](threat-model.md). Runtime Profiles, network policy,
credential separation, fencing, and external authorization continue to apply.

## Allowed dependency classes

Every dependency MUST have one of these classes and a repository-local use:

| Class | Allowed use | Conditions |
| --- | --- | --- |
| Go standard library | Default implementation building block | Use the Go release pinned by `.go-version`. |
| First-party AR package | Code owned by this repository | Follow the domain/application/ports/adapters dependency rule in `PLAN.md`. |
| Versioned ThinkPixel contract | Cross-component OpenAPI, JSON Schema, protobuf, or generated client | Pin a released contract revision; use only through a port/adapter; never import another repository's `internal` packages or access its database. |
| External runtime library | A focused capability needed by shipped AR binaries | Justify it against standard-library or existing-library options; keep provider and protocol types behind adapters. |
| Provider or vendor SDK | Implementation of one replaceable integration | Restrict it to the owning adapter; do not expose its types through domain, application, public API, or neutral port contracts. |
| Build, generation, test, or analysis tool | Reproducible development and CI work | Pin an exact version independently of runtime dependencies; generated output remains reviewable and drift-checked. |
| Runtime service or infrastructure API | PostgreSQL, Kubernetes APIs, Agent Sandbox, OCI, CSI, or another deployed dependency | Record an exact qualified version in `docs/supported-versions.md`; preserve AR authority and replacement boundaries. |
| Agent runtime image content | Harness and packages intentionally installed in an agent image | Keep separate from AR control-plane binaries; pin image/package sources and produce release SBOM/provenance evidence. It remains untrusted sandbox code. |

Convenience alone is not sufficient justification. A new dependency MUST
identify its class, owning package or artifact, purpose, source, selected
version, license, and why the repository does not already provide the needed
capability. A dependency that adds an authority, durable store, required
service, public protocol, or material operational model requires an ADR.

Temporal and other durable workflow engines are prohibited from the MVP/RC
dependency set by [ADR-0004](../adr/0004-no-temporal-in-mvp-rc.md).

## Source and version requirements

Dependencies MUST come from an identifiable upstream project or an approved
internal artifact source. Maintainers MUST be able to establish the source
repository, release identity, license text, and integrity of the fetched
artifact.

- Go modules MUST use canonical module versions recorded in `go.mod` and
  `go.sum`. The Go checksum database or an explicitly governed private-module
  equivalent MUST verify fetched content.
- Release versions are preferred. Pseudo-versions, forks, and unreleased
  commits require a documented reason, upstream revision, owner, and removal
  or update condition.
- `replace` directives pointing outside the repository, local filesystem
  paths, mutable branches, or unreviewed forks MUST NOT be committed. A
  committed replacement requires a time-bounded exception under this policy.
- Tool versions, CI actions, container base images, deployment images, and
  agent images MUST be immutable or exactly pinned. Production OCI images
  MUST use digests; tags may be recorded only as human-readable metadata.
- Source archives, generated binaries, and copied source MUST NOT be committed
  merely to bypass normal dependency resolution. Vendoring requires an
  explicit reproducibility or availability reason and preserves upstream
  license and notice files.
- Generated code MUST record its source contract and generator version. It
  MUST be reproducible and reviewed as code; generation does not transfer
  authority from the versioned wire contract to the generator or SDK.
- Private dependency credentials MUST remain in approved developer/CI secret
  mechanisms and MUST NOT appear in module paths, source URLs, build arguments,
  lock files, logs, evidence, or repository configuration.

Versions with known exploitable vulnerabilities MUST NOT be introduced
without a documented, time-bounded exception and compensating controls.
Dependency updates receive the same tests and boundary review as additions;
major versions and changed module sources require renewed justification.

## License policy

License classification applies to direct and transitive dependencies, copied
code, generated code with licensing obligations, tools redistributed with an
artifact, container layers, and agent runtime contents. SPDX identifiers are
used where available. This policy is an engineering admission rule, not legal
advice; ambiguity is escalated for maintainer/legal review.

### Allowed by default

Unmodified dependencies under the following permissive licenses are allowed
when their notice and attribution obligations are preserved:

- Apache-2.0;
- MIT;
- BSD-2-Clause and BSD-3-Clause;
- ISC; and
- CC0-1.0 or Unlicense for code/data where the upstream provenance is clear.

Public-domain claims without clear provenance are review-required rather than
automatically allowed.

### Review required

The following MUST receive written repository-local approval before merge:

- MPL-2.0 and other file-scoped copyleft licenses;
- LGPL-family licenses, including questions about static or dynamic linking;
- dual- or multi-licensed dependencies where the selected license is not
  unambiguous and recorded;
- licenses with attribution, advertising, patent, trademark, export, data,
  model-weight, or field-of-use terms beyond the default set;
- dependencies with multiple, conflicting, custom, missing, or `NOASSERTION`
  license metadata; and
- copied snippets or generated artifacts whose licensing is unclear.

Approval MUST record the exact dependency/version, selected license, artifact
and linkage/redistribution context, required notices or source-offer actions,
reviewer, expiry or review trigger, and any packaging constraint.

### Prohibited without an explicit legal exception

Dependencies MUST NOT be merged when they are under GPL-family or AGPL-family
licenses, a source-available/non-commercial/no-derivatives/field-of-use
restriction, an unknown license, or terms incompatible with distributing this
repository and its Apache-2.0 release artifacts. An exception requires explicit
legal approval, a documented scope and expiry, and confirmation that all
distribution/source/notice obligations are satisfied. It MUST NOT be used to
weaken an accepted security or architecture contract.

## Review and exception record

A dependency change review MUST include:

1. direct and transitive module/artifact inventory changes;
2. source, version, checksum or digest, and license classification;
3. maintainer/activity and security-advisory review proportional to risk;
4. install/build scripts, code generation, network access, native code,
   privilege, telemetry, and credential behavior;
5. boundary confirmation for domain neutrality, authority, persistence, and
   sandbox placement; and
6. focused tests plus the repository's broadest available verification gate.

Exceptions live in `docs/evidence/` and MUST name an owner, rationale,
affected versions and artifacts, compensating controls, approval, expiry date,
and removal condition. Expired exceptions fail the dependency gate. Security
fixes may be expedited, but their source, license, evidence, and follow-up
review are still recorded.

## Verification and release evidence

The root `make license` and `make vulnerability` targets enforce the automated
Go dependency gate. Analyzer versions are exactly pinned in the root Makefile;
the gate rejects module replacements and unversioned non-main modules, emits a
module and license inventory, enforces the default license allowlist, and
reports reachable known vulnerabilities. Reviewers continue to inspect
non-Go artifacts, image/deployment manifests, generator pins, and the change
diff. The automated checks:

- enumerate direct and transitive runtime/build dependencies;
- reject prohibited or unapproved license classifications;
- detect unexpected module source changes, local replacements, and unpinned
  artifacts;
- report known vulnerabilities without silently suppressing findings; and
- emit reviewable license/SBOM inputs without credentials or private source
  contents.

Release evidence MUST retain tool versions, dependency inventory, license
report, vulnerability disposition, image digests, and applicable notices.
SBOM and provenance generation for released images remains tracked separately
in the release and operations TODOs.
