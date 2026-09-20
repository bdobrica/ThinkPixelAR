# KAS-009 — Coding Runtime Profile template mapping

Implemented deterministic v1beta1 SandboxTemplate rendering from immutable,
schema-validated profile/configuration snapshots. A mandatory trusted callback
validates qualification; unit-test callbacks are synthetic and do not constitute
cluster qualification.

Focused race tests cover exact CPU/memory/storage mapping, immutable image and
entrypoint, architecture selection, hardened Pod/container settings, fixed
Workspace/state/bootstrap/tmp mounts, explicit deny-all template policy and
injection rejection. Substitution tests reject profile/configuration digests,
resource widening, architecture mismatch, mutable image, changed attachment or
bootstrap, invalid claim names and changed mount roots. Copy tests prevent
returned objects from mutating future output. Missing/failed qualification and
warm-pool configuration fail closed.

Validation: `go test -race ./internal/adapters/sandbox/agentsandbox`; `make verify`.
No new dependencies or public schema changes. KAS-010/011/012/014 still own
durable acquisition composition, actual attachment resolution, concrete runtime
qualification and network enforcement. No live profile qualification is claimed.
