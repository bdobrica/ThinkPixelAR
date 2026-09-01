# ENG-015 continuous integration

Reviewed 2026-09-01.

GitHub Actions runs the repository's stable `make verify` baseline on pull
requests and pushes to `main`. Separate jobs build and smoke-test the service
and `thinkpixel-agentd` images so the source verification job does not invoke
Docker. The workflow grants the `GITHUB_TOKEN` only read access to repository
contents, does not persist checkout credentials, sets finite job timeouts, and
neither consumes secrets nor publishes artifacts.

Every third-party action is an official GitHub-maintained action pinned to its
full release commit SHA. The reviewed pins are:

- `actions/checkout` `v6.0.2` at
  `de0fac2e4500dabe0009e67214ff5f5447ce83dd`;
- `actions/setup-go` `v7.0.0` at
  `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`; and
- `actions/setup-node` `v7.0.0` at
  `820762786026740c76f36085b0efc47a31fe5020`.

Dependency caches are disabled in both setup actions. Go build/module, npm,
and temporary directories are placed below a per-run, per-attempt runner
temporary path, preventing pull-request dependencies from being restored into
or saved from a shared persistent cache. `npm ci --ignore-scripts` installs the
exact lock file before the aggregate gate.

This workflow is baseline build evidence only. It has no deployment,
environment, package, identity-token, or release permissions and does not
claim the later integration, Kubernetes, Kata, or release qualification gates.

Local acceptance reproduced every command selected by CI: `make verify`
passed vet, Staticcheck `v0.8.1`, unit and race tests, govulncheck `v1.7.0`
with no vulnerabilities found, dependency/license policy, reproducible binary
builds, and OpenAPI validation/drift checks. `make image-smoke` and
`make agentd-image-smoke` both built the digest-pinned images and passed their
non-root, read-only-filesystem, capability-drop, and no-new-privileges probes.
Static workflow checks confirmed five full-SHA action references, read-only
permissions, and resolution of all three Make targets.
