# ADR-0050: Initialize the pinned Codex child through agentd

Status: Accepted 2026-09-26

## Decision

For bootstrap adapter kind `codex-app-server`, agentd selects the compiled Codex
stdio driver and requires its exact command. Start and Restart acknowledge only
after the stable `initialize` response matches `thinkpixelar/0.155.0` and the
`initialized` notification is written. Experimental API access is disabled.

This extends ADR-0030's generic process launch with the Codex protocol step
anticipated by ADR-0047. Existing command admission, operation replay, fencing,
process-group cleanup and finite startup/shutdown budgets still apply. The
handshake is a local observation, not AR Session readiness or authority.

The child receives private stdin/stdout pipes and a newly created ephemeral home.
Its environment contains only that home, its fresh `CODEX_HOME`, a fixed PATH and
LANG. Nothing is inherited from the supervisor. The home is removed after process
reaping; qualified durable vendor state remains CDX-012. Stderr retains bounded
suppressed capture. Raw protocol frames and initialization metadata do not enter
diagnostics or runtime events.

The driver bounds the response to 64 KiB, validates response identity and rejects
duplicate keys. Cancellation closes the protocol pipes; failed initialization
stops the child. A restart creates fresh pipes, home and process identity and
must initialize again. No protocol fallback, remote listener or automatic retry
is introduced.

## Scope and evidence

[CDX-003 evidence](../evidence/cdx-003-startup.md) covers the real pinned binary
through the supervisor and focused failure/replay checks. Thread creation,
normalized events, full HarnessAdapter conformance and live Kata qualification
remain their existing tasks. The current OCI image must be rebuilt to include
this supervisor change; earlier image evidence does not qualify the new artifact.
