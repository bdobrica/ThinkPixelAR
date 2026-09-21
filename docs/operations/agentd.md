# Agentd startup and bootstrap

The sandbox-local binary now requires closed bootstrap configuration. It is still
an incremental Phase 4 implementation: a configured process waits in
`awaiting_transport` and does not launch a harness or claim Ready. Authenticated
transport is AGD-003–005; process commands follow in AGD-006.

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
