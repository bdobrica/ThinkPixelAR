# Agentd bootstrap Secrets

The Kubernetes bootstrap adapter implements immutable publication, lookup and
exact cleanup. It is not yet wired into the materialization worker or sandbox
credential loader. [ADR-0028](../adr/0028-agentd-bootstrap-secret-projection.md)
records the decision; AGD-005 remains open.

## Trusted composition

Construct `bootstrap.New(client, namespace, trustDomain, records)` with the
existing trusted dynamic Kubernetes client and PostgreSQL `AgentdCredentials` as
the registration checker. Namespace and trust domain are operator configuration.
Use only namespace-scoped `get`, `create`, and `delete` permissions on Secrets;
the sandbox receives no Kubernetes credential or API permission. Kubernetes
Secret storage/access controls remain operator responsibilities.

1. Issue/register a bootstrap through the bounded credential service under current
   authority. Assemble its leaf certificate/key, proof, challenge, separate server
   CA bundle and validated non-secret agentd config. Do not log the material.
2. Call `Plan` and durably reserve the resulting target and cleanup intent before
   creating a Secret. The plan has no UID and cannot be used for projection.
3. Call `Publish`, then durably bind its returned UID. If a known reference is
   returned with an error, use it only for cleanup; do not project it.
4. Resolve the persisted reference immediately before blueprint construction.
   The adapter returns only the Secret name for the existing bootstrap volume.
   The sandbox template mounts it at `/run/thinkpixel/bootstrap`, read-only, 0440.
5. On successful exchange, acquisition failure or expiry, run exact `Delete` from
   durable cleanup work. A conflict requires reconciliation, not an unconditional
   retry by name. Secret deletion does not revoke copied credentials.

After an ambiguous create, `Recover` compares the previously persisted plan to
the observed object before returning its UID. `ErrAbsent` means that exact name
is absent; other failures are not absence evidence. Recovery can inspect an
expired/consumed bootstrap for cleanup, but `Resolve` rejects it for projection.
Do not reconstruct a trusted plan from Kubernetes labels or caller input.

The durable plan/UID store, cleanup scheduler and materialization/loader wiring
in steps 2–5 are still required. The adapter alone does not promise automatic
expiry deletion or cleanup after an AR crash.

## Closed file layout

| File | Ceiling | Purpose |
| --- | --- | --- |
| `config.json` | 64 KiB | Existing closed non-secret agentd configuration. |
| `client.crt` | 16 KiB | Bootstrap client certificate and issuer chain. |
| `client.key` | 4 KiB | Matching ephemeral private key. |
| `server-ca.crt` | 16 KiB, four CAs | Separate expected AR server trust. |
| `bootstrap.proof` | Exactly 32 bytes | One-time bootstrap proof. |
| `challenge.bin` | Exactly 32 bytes | Trusted handshake challenge. |
| `trust-domain` | 253 bytes | Exact configured lowercase DNS trust domain. |

The `agentd.LoadTransport` library entry point now validates and loads all seven
files under [ADR-0029](../adr/0029-agentd-bootstrap-credential-loading.md), pinning
the Kubernetes `..data` generation. Its client-config builder requires a real frame
Check. Destroy owned material only after closing its clients. Binary startup still
consumes only `config.json` until trusted admission/rotation wiring is complete.
Private material stays ephemeral; clear owned delivery buffers after use and
disable raw Kubernetes request/response logging. No CA signing key belongs here.

## Verification

```sh
go test -race ./internal/adapters/sandboxtransport/bootstrap
# With THINKPIXELAR_TEST_DATABASE_URL pointing to a migrated local test database:
go test -race ./internal/adapters/postgres -run TestAgentdCredential -count=1
make verify
```

Tests do not alter the homelab. Live projection, cleanup scheduling and expired
Secret removal remain integration qualification work before enabling the path.
