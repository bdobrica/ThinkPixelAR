# Agentd startup and bootstrap

The sandbox-local binary loads the complete protected bootstrap bundle and runs
the authenticated dispatcher, heartbeat/output forwarding and credential rotation.
Missing credentials, capabilities or a finite control cutoff fail startup. The
[AR hosting runbook](agentd-hosting.md) covers concrete local policy composition,
homelab evidence and remaining live acceptance. Harness launch requires an admitted
command and never establishes trusted Ready state.

`agentd.NewProcesses` validates and copies bootstrap launch configuration.
Generic Start launches the configured direct argv with an empty environment and returns
a local process ID. Stop and Restart require that exact ID; restart returns a
fresh ID. The controller serializes operations without a queue and uses the
configured launch/grace/kill budgets, narrowed by caller deadlines. Stop escalates
SIGTERM to SIGKILL for the group and reaps the leader. A timed-out launch is cleaned
up before another launch is allowed. Start does not imply adapter readiness, and
Stop does not establish full sandbox cleanup. No CLI launch override is exposed.

For `codex-app-server`, the [registered driver](codex-compatibility.md) instead
uses private protocol pipes and a fresh ephemeral home with a fixed environment.
Start/Restart require the pinned initialize/initialized exchange before success;
failure stops the child. This is protocol readiness only, not a vendor thread or
trusted AR Session readiness. The home is removed after reaping.

`NewProcessesWithCapture` opts into stdout/stderr capture; generic `NewProcesses`
retains null standard streams. Codex stdout belongs exclusively to its protocol
driver, while stderr remains suppressed capture. Retrieve `Output(processID)` after Start and consume its
single-consumer Receive stream. Complete adapter records can use EmitEvent;
vendor normalization is still Phase 5 work. Each restart has an independent
capture. Capture uses the existing configured diagnostic/event/queue byte and
count limits. Saturation backpressures producers up to the liveness window;
exhaustion or invalid output terminates capture and the process rather than
silently losing records. Exit draining is bounded by KillWaitMS.

The default sanitizer suppresses whole records with `[REDACTED]`. Only a trusted
registered-schema sanitizer may release selected content; it must remove secret
fields/exact injected values and hidden reasoning, and must be bounded and
thread-safe. Never log captured payloads. Receive returns EOF only after successful
draining; terminal errors require stream-failure handling, not blind restart.
Close abandons and clears capture and stops its live child. See
[ADR-0031](../adr/0031-agentd-bounded-output-capture.md) and
[verification evidence](../evidence/agd-007-output-capture.md).

`Processes.Status` returns a private local process observation without waiting on
Start/Stop or output draining. It includes the opaque process ID, state, observed
exit code/signal and a fixed failure reason. Generic RUNNING means OS process launch;
the Codex path also requires initialization and sets the local `ProtocolReady`
observation. Neither establishes authorized progress. `Processes.Heartbeat` maps current
state into the existing v1 message; wire sequence counters and active operation
must come from the current admitted dispatcher, not the output-capture sequence.

`RunHeartbeats` sends an immediate snapshot and then uses the negotiated interval.
Supply a bounded snapshot function and a sender using the connection's shared
outbound sequencer. The sender must honor its deadline/context and apply current
frame checks. A failed/expired send ends the loop; the owning dispatcher must close
the stream and handle transport loss. Heartbeats are independent of content-queue
backpressure and never extend authority. See
[ADR-0032](../adr/0032-agentd-status-heartbeat.md) and
[status/heartbeat evidence](../evidence/agd-008-status-heartbeat.md).

`NewControls` binds a Processes controller to a copied registry of negotiated
normalized signal handlers and an optional cooperative interrupt handler. Supply
the negotiated command-byte limit; it cannot exceed bootstrap limits. No raw OS
signal number, arbitrary command or unregistered name is accepted. Handlers must
validate their payload schema, honor context and never retain/log input or defer
process writes beyond their return.

Signal/Interrupt require the current local process ID and serialize with process
replacement. A nil Interrupt result means protocol acknowledgement, so the
dispatcher must still observe operation completion. Unsupported/failed/timed-out
interrupts use bounded TERM/KILL Stop; `ErrInterruptEscalated` indicates completed
fallback. A timed-out Signal also stops its target. If a handler ignores its
deadline, replacement remains blocked until it returns. Callback failures expose
fixed errors. Status/heartbeat continue to work while the control slot is busy.
See [ADR-0033](../adr/0033-agentd-signal-interrupt-control.md) and
[control evidence](../evidence/agd-009-signal-interrupt.md). Native Codex mappings
remain Phase 5. Binary dispatch is hosted under ADR-0042; supervisor shutdown follows ADR-0036.

Trusted materialization mounts a dedicated ephemeral bootstrap volume at
`/run/thinkpixel/bootstrap`, read-only, containing `config.json` without write bits. The existing sandbox
template uses mode 0440 with the sandbox group; the non-secret smoke fixture uses
0444.
The opened file must be regular, no larger than 64 KiB, and on a read-only mount.
Projected Secret links confined to that root work; external symlinks do not.
Do not place bootstrap under Workspace/vendor state or inject a Kubernetes token.
The binary rejects CLI arguments, KUBECONFIG and conventional Kubernetes credential
locations. No environment variable can override the endpoint, identity or limits.

The [example](../../deploy/agentd/config.example.json) documents the v1 shape.
Its IDs/digests/endpoint are synthetic test metadata; they cannot authenticate a
transport or authorize work. Trusted materialization must supply actual current
binding and immutable build/adapter expectations. The runnable binary requires the full protected transport bundle and the
additional finite-cutoff/capability requirements in [the hosting runbook](agentd-hosting.md).

| Field | Meaning and bounds |
| --- | --- |
| `version` | Bootstrap format 1. |
| `control_deadline_unix_ms` | Runnable startup requires a future immutable AR materialization cutoff; absence remains decodable for older tooling but cannot start the supervisor. Included in the configuration digest. |
| `endpoint`, `server_name` | HTTPS endpoint with exact lowercase DNS name; optional numeric port 1–65535; no IP literal, userinfo, path, query or fragment. |
| `binding` | Canonical UUIDv7 tenant, Session, Execution, Attempt and SandboxBinding, positive Session generation. Correlation only until AR independently validates it. |
| `build_digest`, `adapter_kind`, `adapter_digest` | Exact immutable protocol/build expectations; validated by the AGD-001 helper. |
| `protocol`, `capabilities`, `required_capabilities`, `limits` | Supported v1 range and closed bounded compatibility metadata; see the [wire reference](../contracts/agentd-protocol.md). |
| `harness.argv` | At most 32 direct arguments, 4096 bytes each and 16 KiB total; no NUL/newline. Executable has a canonical absolute `/usr/bin/` or `/usr/local/bin/` image path; shell/env launchers are rejected. |
| `harness.working_directory` | Exactly `/workspace`. |
| `harness.start_timeout_ms` | Positive, at most 60000. |
| `harness.stop_grace_ms` | Positive, at most 30000. |
| `harness.kill_wait_ms` | Positive, at most 5000. |

Unknown fields, duplicate keys, null values, non-lowercase field names, trailing
JSON and nesting over 12 levels fail. Errors and lifecycle logs contain fixed
messages/state codes; they exclude arguments, configuration, endpoint and paths.
Do not put secrets in argv. Execution-scoped injection will use its own ephemeral
mechanism; this configuration has no environment/credential-value fields.

`make agentd-image-smoke` builds the non-root image and tests bootstrap rejection,
read-only mount acceptance and SIGTERM shutdown with no network, dropped
capabilities, read-only root and no-new-privileges. It creates temporary fixture
files and removes only its own container/fixture. `make verify` runs unit/race
configuration/lifecycle tests. These checks are not yet live Kata transport proof.

## Local checkpoint preparation

Compose `NewCheckpoints(processes, declaredRoots, stateFormat, hook)` only from
the bound runtime and negotiated adapter capabilities. Following authenticated
checkpoint admission, call `Prepare(ctx, processID, consumer)`. The adapter must
flush/quiesce, call its `ready` callback synchronously exactly once, propagate its
error, and release quiescence before returning. The consumer runs within that
window; both callbacks must honor context and avoid deferred work. The existing
`StopGraceMS` bounds their combined duration. Timeout stops the process and holds
the operation gate until the callback returns.

Manifest paths are relative to `/state` (for example `codex/session` for registered
`/state/codex`), sorted and non-overlapping. They are candidate metadata only.
The trusted storage layer must verify actual mounts/files, scan races, sizes,
credential exclusions and integrity before publishing anything durable. Do not
use preparation success as checkpoint COMMITTED, authority or proof of stopped
writes. AGD-020 supplies dispatch; Phase 6 supplies trusted publication. See
[ADR-0034](../adr/0034-agentd-checkpoint-preparation.md).

## Credential exclusion from vendor state

Agentd does not export its environment, bootstrap bundle, home directory or
process image during preparation. Harness start/restart receives an empty
environment and no extra descriptors; bootstrap credentials remain separate from
checkpoint metadata. The local canary regression deliberately lets a child
persist inherited inputs and checks that no credential material reaches state.
See [ADR-0035](../adr/0035-agentd-credential-persistence-exclusion.md).

Future Execution credential injection must use ephemeral runtime files or
descriptors outside Workspace/vendor state, following the
[credential contract](../security/execution-credentials.md). An adapter needing
environment injection must explicitly scope it and extend the exclusion tests.
Do not export an entire home directory or environment as vendor resume state.
The trusted checkpoint producer must still scan and validate actual candidate
files before commit; these local tests do not qualify malicious-harness behavior
or live storage snapshots.

## Supervisor termination

SIGINT/SIGTERM cancels the supervisor lifetime and invokes `Processes.Shutdown`.
Use `RunProcesses(ctx, processes, negotiatedInterrupt, logger)` when composing an
admitted controller. The runner waits for lifetime cancellation and never launches
work itself. Direct AR close may call `Shutdown` with the same registered hook.

Shutdown permanently rejects new commands, cancels pending operations and waits
boundedly for their gate. It attempts cooperative close, then TERM and KILL as
needed. An acknowledgement does not skip process-exit verification. Repeated
calls share cleanup; caller cancellation does not abandon it. Hung callbacks and
unresolved cleanup produce a fixed failure and require external Sandbox release.
Checkpoint preparation is never automatic. PID 1 reaps adopted children after
the managed leader has been reaped; non-PID-1 controllers leave other children
to their owner.

Reserve more than `start_timeout_ms + 3*stop_grace_ms + 3*kill_wait_ms` in the
materialized Pod termination grace, including margin for final reporting. The
example config needs 28 seconds plus margin. AGD-020 must validate that budget
against the effective Pod and deliver the final authenticated observation if the
transport remains available. Current shutdown emits a safe local final state log.
See [ADR-0036](../adr/0036-agentd-bounded-supervisor-shutdown.md).

## Sandbox-local privilege regression

Run `make agentd-image-smoke` on a Linux Docker host. It builds the pinned agentd
image and a temporary static probe matching the image architecture, mounts the
probe read-only, and executes it alongside the real PID-1 supervisor. No probe or
extra privilege is added to the shipped image. The test uses no network access
inside the container and removes its disposable containers and fixture afterward.

The probe checks PID 1 and its own UID/GID, all five capability sets, no-new-privileges
and seccomp filtering. It checks read-only root/bootstrap, conventional Kubernetes
credential and runtime-socket/device exposure, and denial of root UID changes,
raw sockets and tmpfs mounting. The runner independently checks container namespace
configuration and startup rejection of KUBECONFIG and service-account projection.
Failures print fixed messages without credential or process-environment contents.

This is a local image regression, not an attestation by the sandbox. Keep the
external effective-Pod verifier and Kata/network qualification required by
[ADR-0013](../adr/0013-effective-sandbox-security.md). See
[AGD-016 evidence](../evidence/agd-016-privileges.md) for tested scope.
