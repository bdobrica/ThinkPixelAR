# ENG-014 thinkpixel-agentd build and image

Reviewed 2026-08-31.

The root `build` target now produces `thinkpixel-agentd` independently of the
control-plane service and migration binaries. `agentd-image` uses the pinned Go
builder and Distroless non-root runtime already reviewed for ENG-013, but a
dedicated Dockerfile copies only the supervisor binary and declares it as the
entrypoint. No additional dependency or license class is introduced.

The image boundary deliberately excludes Codex and every other vendor harness.
Vendor runtime packages remain under `agent-images/` and are deferred to Phase
5 qualification. The baseline supervisor only owns signal-aware process
lifetime; authenticated transport, harness launch/reaping, diagnostics, and
protocol behavior remain Phase 4 work under the normative
[`agentd` contract](../contracts/agentd.md). It does not hold credentials,
authorize work, or expose a network listener.

The image smoke gate verifies the numeric `65532:65532` identity, exact
supervisor entrypoint, and a live process under a read-only root filesystem,
all Linux capabilities dropped, and `no-new-privileges`. This is packaging
evidence, not Kubernetes, transport, harness, or security qualification.
