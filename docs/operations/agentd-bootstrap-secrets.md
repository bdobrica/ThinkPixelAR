# Agentd bootstrap Secrets

The Kubernetes bootstrap adapter implements immutable publication, lookup and
exact cleanup. It is not yet wired into the materialization worker or sandbox
credential loader. [ADR-0028](../adr/0028-agentd-bootstrap-secret-projection.md)
records the decision; runnable publication/cleanup composition is AGD-020.

## Trusted composition

Construct `bootstrap.New(client, namespace, trustDomain, records)` with the
existing trusted dynamic Kubernetes client and PostgreSQL `AgentdCredentials` as
the registration checker. Namespace and trust domain are operator configuration.
Use only namespace-scoped `get`, `create`, and `delete` permissions on Secrets;
the sandbox receives no Kubernetes credential or API permission. Kubernetes
Secret storage/access controls remain operator responsibilities.

1. Construct the registered credential service and `bootstrap.Delivery` with the
   PostgreSQL journal and a current-authority check. Use the ADR-0040 trusted
   configuration/challenge; never derive them from Hello or Workspace content.
2. Construct `agentdbootstrap.Service` with that issuer and Delivery. Call
   `Materialize` with the exact identity/configuration, separate server CA and
   challenge. It issues once and uses Delivery.Publish: durable plan before
   creation, durable UID before Resolve. Do not call Store.Publish directly.
3. In the trusted callback, persist the returned projection name before acquisition
   and build the read-only 0440 `/run/thinkpixel/bootstrap` volume. Honor the supplied
   credential-bounded context. Failure/cancellation triggers independent bounded
   cleanup; reconcile ambiguous acquisition rather than blindly reissuing.
4. PostgreSQL ConsumeBootstrap atomically requests cleanup when the valid proof is
   consumed. This covers successful acceptance, lost Welcome and failed admission
   rechecks. An invalid proof does not trigger deletion. Deletion is not revocation.
5. Host `agentdbootstrap.NewWorker(delivery, tenants, interval, budget, limit, logger)`
   and `Run(serviceContext)` in the control-plane lifetime. Supply explicit unique
   tenant IDs, interval 5–60 seconds, pass budget at most 30 seconds, batch 1–128.
   A practical initial setting is 5 seconds / 5 seconds / 32. Run sweeps immediately,
   then periodically; failures remain durable and retry without terminating the loop.
   The service binary does not yet host this worker automatically.

Expiry eligibility uses PostgreSQL time and survives restart. Cleanup needs no live
grant. After ambiguous creation, Recover requires the exact persisted ownership and
bundle digest; it binds the observed UID before deletion. Unknown-UID NotFound remains
pending because a delayed create could still arrive. UID/resourceVersion conflicts
require reconciliation; never delete by name alone or adopt a replacement object.
See [ADR-0041](../adr/0041-agentd-bootstrap-lifecycle-cleanup.md) and its
[verification evidence](../evidence/agd-020-bootstrap-lifecycle.md).

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
