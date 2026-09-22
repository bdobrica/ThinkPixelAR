# AGD-011 credential exclusion evidence

Date: 2026-09-22

## Scope and implementation audit

The existing agentd implementation already enforces the local no-copy requirement:

- `process_linux.go` starts/restarts the harness with an explicit empty environment
  and no extra descriptors.
- `credentials_linux.go` reads the fixed protected bootstrap into private memory;
  `credentials.go` exposes non-secret Config separately and clears owned buffers.
- `checkpoint_linux.go` passes copied declared roots and candidate metadata;
  it performs no file copy, environment export or process-state serialization.
- `capture.go` holds bounded, sanitized output in memory without disk spill.
- The only production filesystem open in agentd is the read-only bootstrap reader;
  agentd provides no persistent-state writer.

AGD-011 adds regression coverage and durable documentation, without a new runtime
mechanism, API, dependency or infrastructure requirement.

## Verification

- `go test -race ./internal/app/agentd`: passed.
- `TestAgentdDoesNotPersistCredentials`: runtime-generated canaries in supervisor
  environment and an open transient credential file; decoded transport bootstrap
  with generated proof/key material; a real child attempts to persist inherited
  environment/descriptors. State remains empty apart from the fixture's three
  empty observation files through preparation success, sanitized failure, restart
  and stop. Config/candidate JSON contains none of the tested credential material.
- `TestCheckpointRejectsTransientCredentialLocations`: runtime/bootstrap paths,
  process environment/descriptors and traversal cannot become declared roots or
  candidate manifest paths.
- `TestBootstrapRejectsCredentialPersistenceConfiguration`: environment,
  credentials and Execution token fields are rejected by the closed config.
- `make verify`: passed, including generation checks, formatting, static analysis,
  unit/race tests, vulnerability/dependency checks, builds and API checks.
- Staged diff whitespace and changed Markdown local-link checks: passed.

## Limits

These are local Linux component tests, not live Kubernetes, Codex, credential
injection or snapshot tests. The fixture does not attempt to read parent memory
or access credentials by pathname. An untrusted harness may persist authority
it receives; trusted Phase 6 validation/scanning remains mandatory before
checkpoint commit. Future ephemeral Execution credential injection must extend
this coverage. See [ADR-0035](../adr/0035-agentd-credential-persistence-exclusion.md).
