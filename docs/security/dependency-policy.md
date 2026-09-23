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
advice. The repository owner is responsible for license and distribution
decisions. Coding agents follow the licensing workflow exception in `AGENTS.md`: no
independent legal research or qualification unless explicitly requested. Preserve
supplied notices and metadata, run inventory checks, and record owner decisions
without claiming that those checks or decisions establish legal compliance.

### Allowed when the intended use complies

No license family is categorically prohibited. Permissive, MPL, LGPL, GPL,
AGPL, dual-licensed, source-available and commercial dependencies are eligible
when their actual terms permit the intended use and their obligations can be
met. Routine compliant use needs no separate legal approval, demo exception or
calendar expiry. Maintainers record the selected license and use context during
normal dependency review. The owner decides whether unresolved questions need
specialist advice; coding agents do not initiate that research.

- Preserve applicable copyright, license and notice texts. Record the chosen
  alternative for dual-licensed software and any applicable license exceptions.
- Distinguish independently packaged executables/services from code copied into
  or linked with AR. Sharing an OCI image alone does not require independent AR
  code to adopt the licenses of the other programs. Assess actual integration,
  including communication and linkage, rather than assuming process separation
  always establishes independence.
- Copyleft is allowed. Meet applicable file/source disclosure, relinking,
  installation-information and corresponding-source obligations for the selected
  license and use. Apache-2.0/GPLv3 compatibility does not permit distributing a
  combined GPLv3 work solely under Apache-2.0; GPLv2-only has different compatibility
  constraints. This policy does not relicense AR or remove upstream obligations.
- Local development and internal operation do not require a redistribution
  exception. Before transferring binaries/images to external recipients, privately
  or publicly, provide required notices and corresponding source through a method
  permitted by the applicable license. Source must match the binaries and include
  required patches/build scripts. License identifiers or upstream links alone do
  not establish compliance. Publishing a build recipe is distinct from shipping
  its resulting binaries; copied content in the recipe still has its own terms.
- Apply obligations when their trigger occurs. AGPL can require a source offer
  to network users of a modified covered program without binary distribution.
  Commercial, non-commercial and field-of-use terms must permit the actual use;
  approval cannot override a third party's rights.
- Missing, ambiguous or conflicting license information requires resolution
  before the affected use. A custom license or SPDX LicenseRef is acceptable when
  its actual terms and provenance are recorded. Do not treat missing metadata as
  permission, or block a known compliant license simply because it is unfamiliar.

Distribution compliance is a release responsibility, not a reason to block
otherwise permitted local builds. Outstanding redistribution actions MUST be
recorded before merge and completed before the relevant distribution or network
use. Block only uses whose obligations cannot be met or whose rights remain
unresolved. Security, provenance, version pins and architecture requirements
elsewhere in this policy remain in force.

See [ADR-0049](../adr/0049-use-based-dependency-licensing.md), the
[GNU license FAQ](https://www.gnu.org/licenses/gpl-faq.en.html) and
[Apache compatibility guidance](https://www.apache.org/licenses/GPL-compatibility).

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

Exceptions to engineering/security requirements live in `docs/evidence/` and MUST
name an owner, rationale,
affected versions and artifacts, compensating controls, approval, expiry date,
and removal condition. Expired exceptions fail the dependency gate. Security
fixes may be expedited, but their source, license, evidence, and follow-up
review are still recorded.

## Verification and release evidence

The root `make license` and `make vulnerability` targets enforce the automated
Go dependency gate. Analyzer versions are exactly pinned in the root Makefile;
the gate rejects module replacements and unversioned non-main modules, emits a
module and license inventory, checks inventory completeness, and
reports reachable known vulnerabilities. Reviewers continue to inspect
non-Go artifacts, image/deployment manifests, generator pins, and the change
diff. The automated checks:

- enumerate direct and transitive runtime/build dependencies;
- reject missing or explicitly unknown license metadata; license compatibility
  and fulfillment of obligations remain contextual maintainer/release checks;
- detect unexpected module source changes, local replacements, and unpinned
  artifacts;
- report known vulnerabilities without silently suppressing findings; and
- emit reviewable license/SBOM inputs without credentials or private source
  contents.

Release evidence MUST retain tool versions, dependency inventory, license
report, vulnerability disposition, image digests, and applicable notices.
SBOM and provenance generation for released images remains tracked separately
in the release and operations TODOs.
