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
reaping. SES-006 extends this startup path with the bounded restoration below;
durable export/publication remains CDX-012. Stderr retains bounded
suppressed capture. Raw protocol frames and initialization metadata do not enter
diagnostics or runtime events.

The driver bounds the response to 64 KiB, validates response identity and rejects
duplicate keys. Cancellation closes the protocol pipes; failed initialization
stops the child. A restart creates fresh pipes, home and process identity and
must initialize again. No protocol fallback, remote listener or automatic retry
is introduced.

## Scope and evidence

SES-006 adds an explicit trusted constructor for a newly admitted supervisor.
It imports only one validated, digest-bound Codex 0.155.0 rollout into a fresh
home and selects exact `thread/resume` instead of `thread/start`. It does not
import the previous home, configuration, databases, credentials or process state.
Missing/incompatible state or failed resume stops the child; there is no creation
fallback. Each supervisor imports at most once. Ordinary Restart remains rejected
in thread mode: selecting another checkpoint requires fresh trusted composition,
preventing an implicit rewind after accepted work. Existing admission and turn
fences are unchanged. See [SES-006 evidence](../evidence/ses-006-harness-restoration.md).

SES-005 adds a distinct infrastructure-only probe on quiescent, offline candidate
compute. It resumes with the built-in provider instead of importing an old
Execution's custom provider configuration. This probe client rejects every turn
and rejects changing to ordinary execution mode. The process is stopped after
semantic validation; an admitted Execution restores separately with its own model
route. This changes neither the normal `ResumeThread` parameters nor authority
admission. The candidate's init process reaps adopted descendants between probes.

[CDX-003 evidence](../evidence/cdx-003-startup.md) covers the real pinned binary
through the supervisor and focused failure/replay checks. Thread creation,
normalized events, full HarnessAdapter conformance and live Kata qualification
remain their existing tasks. The current OCI image must be rebuilt to include
this supervisor change; earlier image evidence does not qualify the new artifact.
