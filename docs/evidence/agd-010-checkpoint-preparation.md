# AGD-010 checkpoint preparation evidence

Date: 2026-09-22

## Scope

Immutable declared vendor-root registration and a bounded local preparation hook
under the current process gate. The hook invokes a consumer inside its flush or
quiescence window with a copied, allowlisted relative candidate manifest and
vendor identity/state-format observations. No filesystem persistence or trusted
checkpoint publication is performed.

## Verification

- `go test -race ./internal/app/agentd`: passed. Real child-process fixtures cover
  the preparation window, immutable registration, copied manifest, stale IDs,
  concurrent restart rejection, callback errors/panics, missing/repeated ready,
  invalid manifests and cancellation with a late hook. Lexical tests reject
  traversal, unregistered roots, absolute paths, overlap, duplicates and bounds.
- `make verify`: passed, including generation checks, formatting, static analysis,
  full unit/race tests, vulnerability/dependency checks, builds and API checks.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c` for
  `./internal/app/agentd`: passed (compile only, no live ARM64 execution).
- Staged diff whitespace and changed Markdown local-link checks: passed.

## Limits

No live cluster, vendor adapter or checkpoint storage test was run. A declared
path does not prove safe mounts or contents. Filesystem scan safety, actual size
limits, exclusions and integrity remain trusted checkpoint/storage requirements.
AGD-011 covers credential exclusion work; AGD-020 wires authenticated dispatch;
Phase 5 supplies vendor hooks and Phase 6 publishes durable checkpoints. No new
infrastructure is required for this component test.
