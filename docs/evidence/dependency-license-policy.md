# Dependency license policy revision

Date: 2026-09-23
Decision: [ADR-0049](../adr/0049-use-based-dependency-licensing.md).

The owner requested the widest permitted dependency choice and explicitly directed
coding agents not to perform independent licensing research. The owner makes
bundling/distribution decisions. `AGENTS.md` records that workflow; preserving
notices, package metadata and automated inventory checks remains ordinary work.
No fair-use determination or legal clearance is claimed.

Removed license-family bans, routine legal-approval requirements, the npm
permissive-license allowlist and the CDX-002 calendar expiry gate. Go inventory
validation now rejects missing/unknown license metadata. Pin/integrity checks
remain in place. The original CDX-002 approval and test results remain historical;
its local-only restriction and expiry have been superseded, not renewed.

Validation:

- `make license`: passed.
- `make verify`: passed (protocol generation/drift, hygiene, versions, format,
  vet/static, unit/race tests, vulnerability scan, license inventory, builds and
  OpenAPI checks). Network/cache access was required for pinned Go tooling; the
  initial sandboxed attempt could not resolve the module proxy.
- Eleven isolated npm fixtures: permissive, GPL, AGPL, LGPL, MPL and custom license
  metadata accepted; absent/unknown metadata and absent integrity rejected.
- Five isolated Go inventory fixtures: GPL, AGPL and custom metadata accepted;
  absent/unknown metadata rejected.
- `git diff --check` for changed tracked files: passed.

These checks validate engineering policy behavior, not license interpretation,
image redistribution compliance or fulfillment of corresponding-source duties.
No dependency versions, runtime image contents or public contracts changed.
