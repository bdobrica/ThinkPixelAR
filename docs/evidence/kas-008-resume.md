# KAS-008 — Same-identity provider resume

Date: 2026-09-20.

Resume requires observed suspension (fresh condition and absent backing Pod), or
a replay of the same already-applied resume. It verifies the original immutable
blueprint, rejects deletion/expiry, and sets `operatingMode: Running` through the
durable operation and UID/resource-version guard. The original Sandbox ID and
provider UID remain unchanged. Missing compute returns NOT_FOUND; replacement
requires a distinct Acquire and application-level Attempt fencing. Acceptance
returns RESUMING; effective readiness, fresh authority and a fresh harness
process remain independent requirements.

Focused race tests cover never-suspended rejection, observed suspension,
same-identity resume replay and no replacement when the bound resource disappears.
Live lifecycle checks remain KAS-020.

Passed the final `go test -race ./internal/adapters/sandbox/agentsandbox` and
`make verify` after adding the observed-suspension and revalidation guards.
