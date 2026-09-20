# KAS-005 — Neutral provider observations

Date: 2026-09-20.

Status reads use the tenant-scoped durable binding, exact provider UID and
ownership metadata. Backing Pods must have the expected Sandbox controller owner
reference. Authoritative absence and API unavailability remain distinct errors.

Translation checks condition boolean values and observed generations, desired
operating mode, backing Pod presence, readiness, deletion and terminal phase.
A Suspended condition cannot prove suspension while a Pod still exists. Unknown
modes and unverified effective facts never yield READY. Raw provider condition
messages are not returned as domain reasons.

The mandatory effective-readiness gate is a trusted adapter verifier; without
one, otherwise-ready infrastructure remains UNKNOWN. The verifier must compare
the effective runtime/network/storage/resource/security facts with the immutable
resolution. Its concrete implementation follows in the secure mapping items.

Focused tests cover stale/false conditions, suspend intermediates, terminal
compute, unknown modes, desired-only readiness, verifier gating, and API outage
versus exact absence. This does not claim authenticated agentd transport or
physical isolation qualification.

Passed `go test -race ./internal/adapters/sandbox/agentsandbox` and `make verify`.
