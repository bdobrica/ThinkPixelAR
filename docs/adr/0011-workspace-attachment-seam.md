# ADR-0011: Resolve reserved Workspace attachments without owning their storage

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Sandbox acquisition needs mount inputs, while the Workspace owner controls source
materialization, generations, writer fencing, snapshots and deletion. The blueprint
resolver runs before sandbox reservation and therefore must not mutate storage.

## Decision

Expose neutral Workspace materialization and authoritative attachment-read ports.
The Workspace owner reserves/fences attachment and completes materialization
before sandbox acquisition. A read-only composition validates every tenant,
Session, Execution, Attempt, Sandbox, Workspace and generation identifier against
the saved acquisition request, including operation/configuration evidence.

The Kubernetes Workspace adapter translates opaque volume references only inside
adapters. It reads exact namespaced PVCs and verifies UID, Bound state, filesystem
mode, single-writer access and reserved requested/effective capacities. It requires
independent storage qualification for encryption, snapshots and configuration;
a PVC name or Bound flag cannot qualify those properties.

Feed verified existing Workspace/vendor-state claim names into the immutable
coding mapper. Bootstrap lookup is separate and scoped to the saved Attempt;
credential issuance/consumption stays with the transport owner. Neither blueprint
resolution nor sandbox deletion creates, snapshots or deletes durable storage.

## Alternatives considered

Embedding PVC/CSI fields in neutral attachment types would couple the public/domain
boundary to Kubernetes. Creating storage during blueprint rendering would bypass
durable reservation and make acquisition retries destructive. Trusting namespace
labels alone would permit same-name or cross-generation attachment substitution.

## Consequences

This is the sandbox-facing seam, not a complete WorkspaceProvider implementation.
Production Workspace materialization, authoritative attachment storage and the
transport issuer remain required before service composition can admit work.
Remote Workspace providers can implement the same neutral owner boundary.

## Security

Missing/ambiguous materialization, stale writer fences, mismatched UID/capacity,
shared claims, non-filesystem volumes and unsupported storage qualification fail
closed. Prepared attachment is not proof of a mounted filesystem or authority;
effective mount verification remains separate. No credential bytes cross the
attachment port or enter durable Workspace/vendor data.

## Operations

A provider outage preserves the reservation. Retry the same attachment identity;
never allocate new storage merely because a read timed out. Retain existing PVCs
through compute release/replacement. Verify real storage capabilities separately.

## Compatibility

No public schema change, CSI dependency or new cross-component responsibility.
Concrete volume references remain opaque outside infrastructure adapters. The
initial renderer expects distinct Workspace and vendor-state claims.

## References

- [Workspace contract](../contracts/workspace.md)
- [ADR-0009](0009-coding-template-mapping.md)
- [KAS-011 evidence](../evidence/kas-011-attachments.md)
