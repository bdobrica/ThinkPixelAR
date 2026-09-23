# Hosting AR ↔ agentd on the homelab

The optional AR listener and agentd dispatcher are connected by
[ADR-0042](../adr/0042-agentd-binary-hosting-and-homelab-evidence.md). This is the
bounded transport path for **already admitted and materialized local Executions**.
Creating a Session/Execution through a user-facing API remains later work.

## Prepare the control plane

1. Apply the existing PostgreSQL migrations using `cmd/migrate`. Use the AR database
   and its normal tenant-scoped database role. The listener does not migrate at startup.
2. Provision the dedicated clientAuth-only P-256 issuer and its signing key under
   trusted AR secret management. Provision a separate server certificate/key with
   the listener's DNS name and serverAuth role. Server and client trust must remain
   separate. Keep keys owner-readable only and outside all sandbox mounts.
3. Configure Kubernetes access using the existing `THINKPIXELAR_KUBERNETES_*`
   variables described in [Agent Sandbox operations](agent-sandbox.md). The host
   uses the verified HTTPS connection; agentd never receives these credentials.
4. Prepare the binding's approved blueprint, exact single namespace policy and
   capability-discovery pin from trusted materialization. Persist the local
   authority/materialization snapshot and publish bootstrap using the existing
   [durable coordinator](../adr/0041-agentd-bootstrap-lifecycle-cleanup.md).
   Restarting AR must not register a second snapshot or issue another bootstrap.
5. Configure the trusted evidence publisher described below. Until fresh evidence
   exists, the listener can start but rejects live sandbox admission.

Set `THINKPIXELAR_AGENTD_CONFIG_FILE` to the protected operator JSON file. Its
fields use the Go JSON names listed below; unknown fields/trailing documents are
rejected. Keep its parent directory operator-controlled. Configuration is read once;
change it through a deliberate AR restart. No environment variable contains a key.

| Field | Required value/source |
| --- | --- |
| `Version`, `Mode` | `1`, `"local"`; ThinkPixelAG mode is rejected. |
| `Revision` | Exact SHA-256 revision registered with each materialization. |
| `ListenAddress` | Dedicated host:port, e.g. `0.0.0.0:8443`. Restrict reachability to the sandbox lane. |
| `ServerName`, `TrustDomain` | Provisioned DNS/SPIFFE trust names. |
| `ServerCertificate`, `ServerKey` | Paths to the server's PEM certificate chain and private key. |
| `ClientCA`, `ClientCAKey` | Paths to the single dedicated issuer PEM certificate and signer. |
| `EvidenceDirectory` | Private AR-only directory, mode 0700; never Workspace, bootstrap or a sandbox-writable volume. |
| `Homelab` | Reviewed node name/UID, RuntimeClass UID, and six artifact digests described below. |
| `Discovery`, `CapabilityDigest` | Reviewed `DiscoveryPin` and expected live capability digest; see `deploy/agent-sandbox/homelab-discovery-pin.json`. |
| `Tenants` | Explicit UUID allowlist for cleanup; no tenant discovery. |
| `Bindings` | At most 128 exact binding entries; see below. |

A binding entry contains `TenantID`, `SandboxID`, `RequestDigest`, `Blueprint`,
`NetworkPolicy`, and `Commands`. `Blueprint` is the approved `SandboxBlueprint`
rendered by the existing coding template, not a copy learned from the live Pod.
`NetworkPolicy` includes its exact live UID and reviewed spec. Request digest binds
all profile, runtime, attachment and scope fields. Provider verification compares
both the saved Sandbox and its owned actual Pod against this resolution.

Each optional command has `OperationID`, `ConfigurationDigest`, `HarnessHandle`,
`Kind` (v1 enum integer: START=1, STOP=2, RESTART=3, INTERRUPT=4, STATUS=5), and
`Deadline` (UTC RFC 3339 timestamp). Check the enum in
[agentd.proto](../../api/agentd/v1/agentd.proto) when preparing a plan. Reuse the
persisted materialization's handle/digest, and assign a fresh operation UUID for a
new operation. Compute the configuration digest from the typed configuration with
`agentd.ConfigurationDigest`; hashing pretty-printed file bytes is not equivalent. Commands execute serially with bounded polling. An acknowledged
operation is not sent again; pending/unknown outcomes require reconciliation.
Renewal/reconnect stops local work and does not implicitly restart an acknowledged
START. A later restart needs an explicitly authorized new operation.

Without `THINKPIXELAR_AGENTD_CONFIG_FILE`, AR retains HTTP-only startup. With it,
invalid configuration/dependencies fail startup with a fixed diagnostic. Listener,
HTTP server and cleanup worker share the signal lifetime. The worker sweeps each
configured tenant every five seconds, with a five-second per-pass budget and a
32-record batch. Stream acceptance requests cleanup durably; cleanup is not a new grant.

## Prepare agentd bootstrap

Materialization sets `control_deadline_unix_ms` to its persisted authority cutoff
in milliseconds, before hashing/registering/projecting the configuration. Add
`process-control.v1` and `rotation.v1` to both supported and required capabilities.
Legacy config-only examples deliberately do not constitute a runnable bundle.

Choose `stop_grace_ms + kill_wait_ms < 5000`. AR additionally requires
`start_timeout_ms + 3*stop_grace_ms + 3*kill_wait_ms + 5000` to be strictly less
than the effective profile/Pod termination grace in milliseconds. For a 30-second
Pod grace, 10000/1000/1000 ms satisfies both conditions.

Project all seven credential/configuration files through the established protected
bootstrap mechanism. Agentd rejects partial/mixed/writable bundles. It loads the
bundle, connects outbound over mTLS, rotates bootstrap to a session credential,
and waits for admitted commands. It forwards bounded status/output and heartbeats,
stops on lost transport and keeps replacement credentials only in memory.
The current default capture sanitizer suppresses content until an adapter registers
its schema-aware sanitizer. AR does not yet expose normalized output as Session SSE.

## Trusted homelab evidence publication

**The reader is implemented; automated host collection is not.** A trusted operator
publisher must collect the observations below and atomically publish each receipt.
Do not hand-fill successful observations, reuse fixtures or periodically re-date
old measurements. Without that publisher, secure live admission stays unavailable.

The initial reader deliberately supports the qualified `coding-homelab-arm64` lane:
`kata-qemu-runtime-rs-ar331-bounded`, 1 CPU/512 MiB/128 hard processes, 32 MiB
bounded scratch, with RuntimeClass overhead 250m/2304 MiB. It does not certify a
different PC/amd64/runtime/storage configuration. No cluster installation, worker
mutation or new paid infrastructure is performed by the listener.

The publisher needs trusted worker and Kubernetes access, separate from agentd.
For each currently running owned Pod:

1. Resolve its exact Pod UID/container ID/CRI sandbox ID and observed node UID.
   Use the read-only [host proof](../../test/security/kata-host-proof.py) to match
   the live QEMU process, start ticks, KVM VM descriptor and qualified QEMU/kernel/
   guest-image hashes. Independently fingerprint the runtime binary, effective
   runtime configuration and CNI installation. Compare to the reviewed `Homelab`
   pin (`Node`, `NodeUID`, `RuntimeClassUID`, `Artifacts`). `Artifacts` has exactly
   `qemu`, `kernel`, `image`, `runtime`, `configuration`, `cni`, each `sha256:<hex>`.
2. Resolve the requested immutable OCI reference to its approved ARM64 manifest
   using trusted runtime/registry evidence. Record that manifest and compare the
   actual container image identity. An arbitrary image ID is insufficient.
3. Observe the effective CPU quota/period, memory maximum and hard NPROC ceiling
   through trusted instrumentation, tied to the exact running container and
   qualified runtime. Retain the measurement record and its SHA-256 digest.
   [Resource qualification](kata-resource-checks.md) describes the measured lane.
4. Observe current host/CNI enforcement for the exact namespace, Pod and permitted
   control-plane/DNS peers. Prove protected destination denial and selected DNS
   semantics. Historical TCP-only tests do not prove every guarantee. Retain this
   measurement's digest; stop publication if policy/CNI/peer identity changes.
5. Verify the actual `/workspace`, `/state` and bounded `/tmp` backing/mounts,
   exact PVC/PV identities, current Attempt/workspace fence and absence of another
   writer. Retain the mount/fencing measurement digest. Kubernetes capacity alone
   is not physical quota proof; use [bounded scratch](bounded-scratch.md).
6. Assemble `HomelabEvidence` from these observations and the exact persisted AR
   scope/request/provider reference. Its complete typed format is in
   [homelab.go](../../internal/adapters/sandbox/agentsandbox/homelab.go).
   `LiveObjects` contains the namespace, its single NetworkPolicy, and the three
   PVC/PV pairs, each with resource/name/namespace/UID/resourceVersion. The reader
   re-fetches every object and verifies the scratch owner and volume bindings.
7. Set `ObservedAt` to the beginning of the observation interval and `ExpiresAt`
   no more than 30 seconds later. Publish only if the complete interval remains
   valid; otherwise collect again. Write a new regular 0600 file and atomically
   rename it to `<pod-uid>.json` under the private directory. The file and directory
   must be owned by root or AR's effective UID. Retain referenced measurements in
   trusted operator evidence storage, without credentials or runtime payloads.

AR never writes these receipts or refreshes their timestamps. Each admission/frame
reopens the receipt and checks current API identity; missing, expired, oversized,
foreign-scope and changed-object evidence fails closed. A successful read supplies
infrastructure facts only. The database still decides authority, fencing, epoch,
revocation, deadline and durable command replay.

## Acceptance still required

The [integrated application test](../evidence/agd-020-integrated-acceptance.md)
passes with real agentd, mTLS and PostgreSQL policies; it includes reproduction
commands and explicitly identifies its Kubernetes provider/API fixtures.

Run one governed-by-local-policy controlled-process exchange with the durable
PostgreSQL policies and freshly observed homelab evidence. Include rotation,
transport loss, AR restart, credential expiry and fenced sandbox replacement.
Validate final stop reporting and durable safe-replacement admission. Record results under
[Phase 4 evidence](../phase-4-evidence.md) before closing AGD-020/019. The local
fixture tests establish implementation behavior; they do not qualify the live publisher.

## Failure handling and pending replacement

The host now sweeps each configured binding for expiry of the latest persisted
credential and connection. A five-second per-binding call budget bounds each
pass; passes start immediately and then every five seconds (slow calls can extend
a full pass). Transient disconnection and expiry of predecessor credentials do
not trigger this decision. Expiry atomically fences the binding, records recovery
and exact cleanup intent, and degrades the current Session. Saved release intents
are retried through the existing provider with the same operation ID after AR
restart. Provider failure leaves cleanup pending; uncertainty never becomes a
successful deletion report.

Inspect pending `sandbox.recover` work and the existing cleanup intent state.
A recovery request is **not a running replacement**. Ambiguous command outcomes
remain PENDING/UNKNOWN until a durable safety decision is implemented; do not
manually replay the command plan, restart agentd with its old bundle or mark that
work complete merely because the old Pod disappeared. Automatic new-Attempt
admission remains implementation work.

On graceful local cancellation, an established transport can carry a bounded
`process-control.v1/shutdown` hint after managed-process stop. Its authenticated
receipt is logged as `agentd final stop observation`. `managed_process_stopped`
is a sandbox claim, not canonical lifecycle state. Abrupt disconnection or expired
authority can prevent delivery. Agentd always performs bounded supervisor cleanup
and emits `agent supervisor stopped` or `agent supervisor cleanup failed` locally.
See [ADR-0043](../adr/0043-agentd-failure-fencing-and-final-observations.md).
