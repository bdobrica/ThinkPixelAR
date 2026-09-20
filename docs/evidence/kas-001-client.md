# KAS-001 — Kubernetes client boundary

Date: 2026-09-20.

Implemented explicit in-cluster/development connection configuration and the
adapter-local client-go port. Dependency justification: official kubeconfig,
credential/TLS handling and dynamic API access are required by the sandbox and
Workspace adapters; implementing these protocols locally would duplicate
security-sensitive upstream behavior. client-go/apimachinery are exactly
`v0.36.2`; Kubernetes types remain in the infrastructure adapter. New transitive
licenses were inspected and recorded in `build/dependency-licenses.tsv`.
The protobuf pseudo-version is the exact minimum selected by client-go.

Validation passed:

- `go test ./internal/config ./internal/adapters/kubernetes`: both modes,
  explicit context forwarding, invalid settings, credential-error redaction,
  verified TLS requirement, actual HTTPS timeout, source-config immutability.
- `make verify`: hygiene, version/format checks, vet, Staticcheck, all unit and
  race tests, vulnerability scan, license inventory, builds, OpenAPI drift.

An initial Staticcheck failure for capitalized error strings was corrected and
the complete gate rerun. Commands required access to external Go caches/network.
Existing unrelated worktree changes were retained and are excluded from this
commit. This evidence does not claim live provider or isolation qualification.
