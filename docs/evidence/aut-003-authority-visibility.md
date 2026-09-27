# AUT-003: local authority visibility

Implemented 2026-09-27 for the existing LocalAuthority adapter and HTTP service.

`GET /authorityz` returns safe deployment metadata from the adapter attached by
trusted service composition. For LocalAuthority the response is:

```json
{"authority_mode":"local","authority_issuer":"thinkpixelar/local","description":"Standalone local authority; no ThinkPixelAG governance."}
```

No attached adapter returns `authority_mode=unconfigured` and an empty issuer.
The current `cmd/thinkpixelar` has no Execution authority composition and therefore
reports this explicitly. There is no environment/request-header fallback to local
mode. AG adapter metadata identifies configuration only, not AG availability or
admission. This coarse endpoint has the same unauthenticated diagnostic access
boundary as health endpoints; it exposes no grants, tenants, policy or content.

LocalAuthority construction emits an operator-visible warning. Admission,
validation and cancellation logs and optional traces carry `authority_mode=local`
and `authority_issuer=thinkpixelar/local`, including rejected/failed operations.
Successful validation/cancellation includes the observed `authority_state`.
Operation `result=success` means the call succeeded, not that a grant is ACTIVE:
admission can replay an expired grant and validation can return terminal status.
Inputs, grant snapshots, policy data and raw errors are never passed to sinks.

Supply `local.Observability` with the service logger, tracer and metrics, and pass
that same authority instance as `http.Options.Authority`. The default adapter
logger still emits mode labels and the startup warning without optional sinks.
`thinkpixelar_authority_info{authority_mode="local"} 1` identifies the selected
deployment mode; the other two bounded modes have value zero. Existing latency
metrics retain their `mode` label for compatibility. Issuer and object IDs are
never metric labels.

Verification: focused Go race tests for local authority, HTTP and telemetry;
PostgreSQL local admission/lifecycle race tests with migration 22 in an isolated
test database, including successful admission/ACTIVE telemetry; pinned Redocly
2.49.0 OpenAPI lint, bundle generation and byte-for-byte regeneration check;
affected `go vet`, embedded OpenAPI tests and command package compilation. The
isolated database was removed.

No grant snapshot format, migration, admission decision or lifecycle rule changed.
The endpoint is deployment metadata, not a replacement for each Execution's
immutable authority binding. Execution API/SSE/evidence composition remains
SES-001 / EXE-001–002 and must expose the persisted grant's mode/issuer, never
infer historical authority from `/authorityz`. This change does not implement an
AG adapter, public admission routes, or claim full RunAuthority conformance.
