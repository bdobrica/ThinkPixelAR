# KAS-023 — Agent Sandbox compatibility assumptions

Date: 2026-09-20. Documentation closure only; not resource or Phase 3 qualification.

The [supported-version policy](../supported-versions.md) now matches ADR-0006,
ADR-0010 and ADR-0014 and the actual implementation/evidence:

- Agent Sandbox v1.0.0 core beta API, direct cold acquisition, same-name owned Pods,
  observed-generation conditions, absolute shutdown, exact-UID foreground release,
  native suspend/resume and explicit Service disablement;
- template/claim API source support distinguished from deployed extensions;
- exact K3s/containerd/Kata ARM64 test tuple distinguished from older candidates,
  untested amd64 hardware and complete release qualification;
- capability-dependent storage requirements rather than a universal paid encrypted
  snapshot backend prerequisite for homelab RC work;
- repeatable update review, immutable pinning, conformance/live checks, stored-version
  migration and rollback requirements. A fresh install does not certify upgrades.

The version/API statements were checked against the local pinned adapter/API types,
existing manifests and live evidence, accepted ADRs, and the
[upstream v1.0.0 release](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0).
The Phase 0 review remains historical; an appended current disposition explains
what has passed and why complete qualification is still open.

KAS-022's actual scratch failures remain blockers. Startup capability/discovery
validation is still incomplete; the documentation explicitly does not claim it
exists. Production composition, bounded storage and authenticated agentd integration
must be completed before end-user secure admission. No contract or accepted ADR
was weakened to call the current candidate release-qualified.

Validation: `make versions-check`, focused adapter/API race tests, documentation
link checks for changed documents and `make verify` passed. No new live deployment,
upgrade or production infrastructure test was performed for this documentation item.
