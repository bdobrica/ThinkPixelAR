# Architecture Decision Records

Architecture Decision Records (ADRs) capture decisions that constrain ThinkPixelAR's implementation or operation.

## Lifecycle

1. Copy `template.md` to a zero-padded sequential filename.
2. Set status to `Proposed` and open the decision for review.
3. Set status to `Accepted` only after alternatives, security, operations, and compatibility effects have been evaluated.
4. Do not rewrite an accepted decision to change its meaning. Create a new ADR with status `Supersedes ADR-NNNN`, and mark the old record `Superseded by ADR-NNNN`.
5. Use `Rejected` for a proposal that was considered but not selected and `Deprecated` when a decision no longer applies without a direct replacement.

The allowed base statuses are `Proposed`, `Accepted`, `Rejected`, `Deprecated`, and `Superseded`.

