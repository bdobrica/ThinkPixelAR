# KAS-007 — Provider suspension

Date: 2026-09-20.

The pinned v1.0.0 API uses `spec.operatingMode: Suspended`. The adapter records
the stable operation/revision, verifies the exact binding, and applies a merge
patch guarded by UID and resourceVersion. Same-operation replay does not issue
another patch. A later operation supersedes older replay. It never treats desired
mode as completed suspension: KAS-005 requires a fresh true Suspended condition
and absent backing compute. Workspace creation/deletion and checkpoint guards
remain outside this provider operation.

Passed focused race tests for the actual upstream field, one-patch replay, and
superseded operations, plus the full `make verify` gate. Live suspend semantics
remain separately required by KAS-020.
