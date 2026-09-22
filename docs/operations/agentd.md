# Agentd startup and bootstrap

The sandbox-local binary now requires closed bootstrap configuration. It is still
an incremental Phase 4 implementation: a configured process waits in
`awaiting_transport` and does not launch a harness or claim Ready. The [authenticated transport adapter](agentd-transport.md) is implemented;
[Credential issuance and renewal](agentd-credentials.md) are implemented; trusted
binary admission/delivery composition remains AGD-020; rotation/reconnect is AGD-013. Process commands
are implemented locally under [ADR-0030](../adr/0030-agentd-bounded-process-control.md)
and await authenticated binary dispatch in AGD-020.

`agentd.NewProcesses` validates and copies bootstrap launch configuration.
Start launches the configured direct argv with an empty environment and returns
a local process ID. Stop and Restart require that exact ID; restart returns a
fresh ID. The controller serializes operations without a queue and uses the
configured launch/grace/kill budgets, narrowed by caller deadlines. Stop escalates
SIGTERM to SIGKILL for the group and reaps the leader. A timed-out launch is cleaned
up before another launch is allowed. Start does not imply adapter readiness, and
Stop does not establish full sandbox cleanup. No CLI launch override is exposed.

`NewProcessesWithCapture` opts into stdout/stderr capture; `NewProcesses` retains
null standard streams. Retrieve `Output(processID)` after Start and consume its
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
exit code/signal and a fixed failure reason. RUNNING means OS process launch,
not adapter readiness or authorized progress. `Processes.Heartbeat` maps current
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
remain Phase 5; binary dispatch is AGD-020 and supervisor shutdown is AGD-012.

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
binding and immutable build/adapter expectations. Transport credential loading
and bootstrap exchange are separate later implementation steps.

| Field | Meaning and bounds |
| --- | --- |
| `version` | Bootstrap format 1. |
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
