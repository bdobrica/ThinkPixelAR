# ADR-0037: Agentd credential rotation, reconnect and recovery

Status: Accepted 2026-09-22

## Decision

AGD-013 implements the existing ADR-0002 rotation/reconnect contract using the
credential registry and v1 Rotation messages. No new wire fields, schema migration,
dependency or infrastructure service is required.

An empty Hello proof selects reconnect. A 32-byte proof selects one-time
bootstrap consumption. Reconnect requires the latest registered, unexpired
certificate with exact identity/expiry; bootstrap certificates must already have
been consumed. Each acceptance atomically assigns a fresh connection ID and
increments the durable epoch under the current aggregate fence. Prior frame
checks fail, and old Close callbacks cannot erase a replacement. The gRPC server
consults durable admission before replacing a local stream, rejects equal/older
epochs and cancels an older local stream only after acceptance. Replica restart
requires no process-local credential or epoch state.

The optional negotiated `rotation.v1` capability enables existing Rotation fields.
Client REQUEST contains no certificate, key or expiry. The trusted handler calls
agentdidentity.Rotate with Session.Peer and the accepted Welcome ID/epoch, never
payload-selected authority. It registers a fresh key before returning ISSUED.
The handler sends on that stream and clears reply buffers. Registration caps the
predecessor's remaining frame authority at 30 seconds; only the latest credential
can reconnect or renew. Failed/ambiguous delivery cannot reuse the predecessor to
mint another replacement.

Agentd validates received certificate/key bounds, signatures, client identity,
role separation, maximum 15-minute lifetime, exact advertised expiry and a fresh
public key. Installation requires its current admitted connection/epoch and stays
in memory. The authenticated AR sender supplies the client issuer chain; it does
not alter the pinned server trust roots. AR independently validates client issuer
trust. The bootstrap proof is cleared after the first connection attempt, including
ambiguous failure; it is never replayed to recover a lost Welcome.

RunConnections requires a finite authority/freshness deadline and explicit frame,
stream and disconnect hooks. The stream owner serializes outbound frames, handles
the renewal timer and installs received replies. Bootstrap requests renewal
immediately; session renewal is due 30 seconds before expiry. Stream operation
ends five seconds before its effective deadline to allow local stop/fencing.
Disconnect cleanup has a five-second context and precedes retries. Hooks are
trusted code and must honor cancellation; no permit-all default or offline command
queue exists. Short unsuccessful sessions count toward eight reconnect attempts;
backoff is jittered, exponential and capped at five seconds. Successful rotation
or a stable session resets the burst. Credentials are destroyed when the loop ends.

## Expired identity recovery

Recovery issuance is an explicit control-plane operation, separate from renewal.
RecoverBootstrap policy must approve a safely recoverable current Attempt; provider
identity/effective facts and authority are rechecked before signing and registration.
The registry permits replacement bootstrap only after the latest certificate and
connection deadline expire, checks the optimistic version, clears the old connection
and advances its epoch atomically. A fresh one-time proof and certificate must then
be delivered through a protected provider mechanism and consumed normally.

Agentd first stops/fences local work and waits for actual expiry. It may load one
fresh, matching configuration/Attempt bootstrap through the supplied recovery
loader. It cannot issue credentials or read Kubernetes credentials. Missing,
invalid or unsupported recovery fails closed. The current Kubernetes immutable
bootstrap projection does not support in-place refresh: composition must fence
and replace the Sandbox unless a protected recovery delivery mechanism is
available. This change does not make immutable Secrets mutable or add exec tunnels.

## Composition and evidence

AGD-020 retains concrete authority/frame policies, binary dispatch, heartbeat/event
multiplexing, provider delivery and cleanup orchestration. Its stream handler must
use the renewal timer, the current Session identity and the local stop hook; final
wiring remains mandatory before Phase 4 exit. Provider-qualified same-Pod recovery
is not claimed. The implemented fallback is denial/local stop, followed by trusted
AR reconciliation/replacement.

Tests exercise real loopback mTLS rotation and reconnect, replacement AR server
instances, lost-Welcome proof suppression, expiry recovery loading, policy/provider
denial, and real PostgreSQL epoch replacement, concurrency, expiry and recovery.
See [AGD-013 evidence](../evidence/agd-013-rotation-reconnect.md).
