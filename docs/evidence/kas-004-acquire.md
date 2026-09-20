# KAS-004 — Acquire operation

Date: 2026-09-20.

Implemented the neutral acquisition/binding types and
`KubernetesAgentSandboxProvider.Acquire`. It validates immutable images and
operation digests, resolves a deterministic approved blueprint, durably reserves
the exact Attempt binding before external mutation, creates by stable AR identity,
and records the exact provider UID. A matching replay observes the same resource;
changed digests, UID substitution or blueprint mutation fail closed. Absence of a
previously UID-bound resource does not recreate that physical Attempt.

Acquisition returns PROVISIONING; it never infers readiness from creation.
The trusted blueprint resolver must resolve profile/Workspace/bootstrap
references and capabilities; no raw caller manifest crosses the neutral port.
Provider storage templates and inbound Services are rejected. Production binding
storage and concrete template mapping remain KAS-009/010; the service is not wired
to acquire until those dependencies and the remaining readiness/security gates
are implemented. The test binding store is test-only.

Focused race tests use an HTTP Kubernetes API fixture to verify replay after
response loss, concurrent acquisition, reconstruction by another provider
instance, conflicting request digests, immutable UID/spec checks, bound-resource
absence, and reservation failure before create. No live-provider claim is made.

Passed `make verify` and the final focused
`go test -race ./internal/adapters/sandbox/agentsandbox`.
