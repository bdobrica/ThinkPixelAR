# KAS-014 — Network enforcement hooks

Date: 2026-09-20

Implemented acquisition/resume/readiness hooks and a read-only dedicated-namespace
policy verifier. See [ADR-0015](../adr/0015-sandbox-network-enforcement.md).
The operator installs policy before compute admission; no workload-supplied rule
or broader fallback is accepted. Policy remains installed during compute release.

Verification covers missing enforcers, reservation-before-enforcement ordering,
no compute creation on enforcement failure, replay after correction, additional
policies, UID replacement, spec widening, missing policy, pagination, namespace
and digest mismatch, missing physical proof, and unbounded selectors/ports.
Positive unit qualifiers are synthetic and do not establish live CNI behavior.

Commands: `go test -race ./internal/adapters/sandbox/agentsandbox/...`; `make verify`.
Live metadata/API denial remains KAS-015. Production composition must supply
trusted continuous network qualification and the later reconciler must fence on
drift; policy inspection does not implement credential revocation by itself.
