# KAS-002 — Pinned Agent Sandbox APIs

Date: 2026-09-20. Decision: [ADR-0006](../adr/0006-agent-sandbox-v1-api-pin.md).

The API module is exactly `sigs.k8s.io/agent-sandbox v1.0.0`, from upstream
commit `bb72f49d79f009a960eed2ae6c32e1cc082399c5`, module sum
`h1:ig1d1kePwOnQUel4GkkO4MRln38Yn73Rj+OhPKVKVLk=`.
Only API packages are imported; no upstream controller or router is embedded.
The core and extension beta schemes register the required resources. The module
requires Kubernetes v0.36.4; module minimum-version selection and the license
inventory were updated accordingly. API module licensing is Apache-2.0.

Passed `go test ./internal/adapters/kubernetes ./internal/adapters/sandbox/agentsandbox`
and `make verify`, including static/race/vulnerability/license/build/OpenAPI gates.
The boundary test walks Go source and rejects Kubernetes/Agent Sandbox imports
outside adapters. These are source checks, not live controller qualification.
`PH0-KAS-001` remains open for lifecycle, storage, network and isolation evidence.
