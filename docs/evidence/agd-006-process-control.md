# AGD-006 — bounded process control

Implemented 2026-09-22 under [ADR-0030](../adr/0030-agentd-bounded-process-control.md).

Real Linux child-process race tests passed five consecutive runs:

```sh
go test -race ./internal/app/agentd -run '^TestProcess' -count=5
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/agd006-agentd-arm64.test ./internal/app/agentd
```

The test binary supplies a deterministic child through test-only argv. Tests
verify an empty child environment despite a parent canary, exact process-ID
checks, one winner among concurrent starts, a fresh ID after restart, idempotent
stop, and reaping the previous leader before replacement. A child ignoring
SIGTERM is killed. A cancelled restart never launches its replacement; an
undelivered launch result triggers cleanup. Invalid config and missing executable
fail with fixed errors. Leader exit kills a remaining same-group descendant.
The ARM64 test binary cross-compiled successfully; it was not executed on ARM64.

The tests distinguish a stopped orphan zombie from a running descendant; reaping
adopted children is PID 1's responsibility. This is local process evidence, not
proof of full sandbox credential cleanup or a live Kubernetes/Codex deployment.
Start means process launch, not a successful harness protocol handshake.

The final `make verify` passed: generated-artifact checks, hygiene, version and
format checks, static analysis, repository-wide unit/race tests, vulnerability
and dependency checks, binary builds and OpenAPI verification. Staged whitespace,
repository hygiene and changed Markdown local-link checks also passed. No paid
infrastructure, cluster modification, dependency or public contract change is
required. Binary dispatch remains AGD-020; stdout/stderr capture is AGD-007 and
supervisor shutdown/reaping composition is AGD-012.
