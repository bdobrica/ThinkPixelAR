# KAS-011 — Workspace attachment/materialization seam

Added neutral AttachRequest/Attachment values, the narrow Materializer port and
an authoritative AttachmentReader port. The sandbox adapter composes exact scope
validation, read-only Kubernetes volume resolution and protected bootstrap lookup
with the coding mapper. No Kubernetes or CSI types enter domain/public contracts.

Tests use HTTP-backed Kubernetes clients and prove exact scope checking before
provider/bootstrap reads; successful repeated composition; no storage mutations;
UID/namespace substitution rejection; missing/pending claims; access-mode,
filesystem/capacity drift; unsupported encryption/snapshot evidence; ambiguous
materialization; and qualification failure. Unit qualification callbacks are test
fixtures, not actual encryption or CSI qualification evidence.

Validation: focused Workspace/sandbox race tests and `make verify`.

This completes the sandbox-facing seam. The full WorkspaceProvider, durable
attachment owner, source initialization, snapshot/restore and transport bootstrap
issuer remain their separately tracked implementation work. No storage or Secret
was created on the homelab, and no full secure-profile admission is claimed.
