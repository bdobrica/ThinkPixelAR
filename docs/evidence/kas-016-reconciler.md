# KAS-016 — Compute reconciliation

Date: 2026-09-20.

Implemented the application reconciler and neutral intent, observation, provider
and authority ports. [ADR-0016](../adr/0016-compute-reconciliation.md) records the
boundary. PostgreSQL worker scheduling and recovery effects follow in KAS-017/018;
this item does not claim a running service loop or completed Session restore.

Tests cover original-operation replay after ambiguous acquisition, absence of a
previously bound resource, provider outages/timeouts, verified/unverified READY,
UNKNOWN and failed compute, integrity drift, release acceptance versus confirmed
absence, cleanup with expired execution authority, tenant/current-authority guards,
and rejection when observation compare-and-swap loses a race. Provider payloads
are reduced to bounded reason codes. No Session or Execution terminal writer is
available to the reconciler.

Verification: `go test -race ./internal/app/reconciliation/...`; `make verify`.
