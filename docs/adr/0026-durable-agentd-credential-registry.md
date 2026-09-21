# ADR-0026: Durable agentd credential registry

Status: Accepted 2026-09-21

## Context

ADR-0024 requires persisted identity and epoch checks. ADR-0025 deliberately
requires durable registration before returning issued credentials. Process-local
maps cannot prevent bootstrap replay across AR replicas or restarts.

## Decision

The provider-neutral `CredentialRegistry` port separates durable identity
bookkeeping from provider/Run authority and wire admission. Its PostgreSQL adapter
shares AR's tenant-scoped SandboxBinding and Session/Execution/Attempt model.
It does not implement `Authorizer` or `CredentialAuthority` by itself. Trusted
composition must still revalidate current external authority, provider facts,
revocation and rate limits before registration and admission.

Migration 19 adds two RLS-protected tables: a per-binding credential/connection
cursor and retained credential records containing only IDs, digests and validity.
The cursor has a composite foreign key to the exact tenant/Attempt/SandboxBinding.
Credential records have immutable identity/validity and permit only one bootstrap
consumption timestamp. Neither table stores PEM, private keys, plaintext proof,
handshake payload or other runtime content.

Registration uses the expected cursor version as a compare-and-swap, enforces
certificate validity and grant deadlines, and rechecks the persisted aggregate
fence under row locks. One initial bootstrap can be registered per binding.
Renewal additionally requires the current certificate and connection ID/epoch;
competing registrations from the same version cannot both commit. Issuance of a
successor does not itself change the existing TLS connection's identity.

Bootstrap consumption verifies the registered certificate digest and exact expiry,
compares the 256-bit proof's SHA-256 digest, and atomically marks it consumed while
allocating a random connection ID and incrementing the persisted epoch. Wrong
proof/identity rolls back without burning the valid proof. A completed exchange
is never made reusable by a later stream failure or close. Admission also requires
a provider reference; its effective security facts remain a separate mandatory
trusted check.

Connection checks revalidate the binding and current aggregate fence as well as
the exact certificate, connection ID, epoch and database deadline. Closing clears
only the specified epoch and remains possible after cancellation. SQL/database
errors are exposed through one fixed credential-state error.

Lock order follows the existing provider: immutable binding lookup, aggregate
fence locks, binding rows, then credential rows. No external provider/authority
call occurs inside the registry transaction.

## Consequences

This is an AGD-005 foundation checkpoint, not completed admission composition.
AGD-005 remains open for current provider/authority adapters, immutable handshake
expectation loading, per-frame checks, cleanup scheduling, protected Secret
delivery and authenticated rotation wiring. The binary remains dormant.
Recovery bootstrap and reconnect epoch replacement remain unwired; no in-memory
fallback or broad recovery exception is introduced.

Real PostgreSQL tests cover cross-binding denial, bootstrap races/restart replay,
competing renewal registration, stale close, cancellation, immutable history and
RLS under a non-superuser role. See [evidence](../evidence/agd-005-registry.md).
