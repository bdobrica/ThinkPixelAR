# ADR-0030: Agentd bounded process control

Status: Accepted 2026-09-22

## Decision

AGD-006 supplies the Linux `agentd.Processes` controller with Start, Stop and
Restart operations. Construction validates the existing bootstrap Config and
copies its harness argv. Commands cannot substitute argv, working directory,
environment or executable. Non-Linux construction fails closed.

Launch uses direct exec in a new process group, with the configured `/workspace`
working directory, an empty environment, no extra inherited descriptors and
standard streams attached to the null device. Output capture belongs to AGD-007;
approved environment/credential injection must be added explicitly by its later
composition. Nothing is logged from the child or OS errors.

One operation is admitted at a time with no waiting queue. A running or unresolved
child blocks another Start. Stop/Restart require the exact current process ID;
successful launch generates a fresh opaque UUIDv7. This local ID is not an AR
HarnessHandle or execution authority. Start reports OS launch, not adapter Ready.

Stop sends group SIGTERM, waits the configured grace, then sends group SIGKILL
and waits the configured kill budget. Cancellation accelerates escalation.
Restart stops/reaps the previous leader before launching a new one. Stop is
idempotent for the retained current ID; an old ID cannot stop its replacement.

Calls return within the caller deadline or configured operation budget. An OS
launch cannot always be interrupted; its worker retains the operation gate until
it returns and cleans up any child whose launch result was not delivered. There
is at most one such worker. Unresolved cleanup blocks another launch. Errors are
fixed codes with no executable paths, argv or environment values.

On leader exit, Linux waitid with WNOWAIT retains its PID before signalling any
remaining group members with SIGKILL; only then does exec.Wait reap the leader.
Group signals and final reaping are serialized to avoid signalling a reused PID.
This kills same-group descendants but does not attest full process-tree cleanup:
PID-1 orphan reaping and graceful adapter shutdown remain AGD-012, and escaped
processes require external sandbox containment/destruction. Successful local
Stop is not permission to issue a new Execution credential.

## Consequences

Real child-process tests cover start/stop/restart, stale IDs, concurrency,
environment isolation, cancellation, launch failures, escalation and remaining
group members after leader exit. No shell fixture or runtime dependency is added.

The binary remains dormant until AGD-020 composes authenticated dispatch and
command-specific checks. AGD-009 supplies protocol interrupts, AGD-012 supplies
supervisor shutdown integration and AGD-017 supplies the wider test adapter.
No wire/schema or cross-component ownership changes are introduced.
