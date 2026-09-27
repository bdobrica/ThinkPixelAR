# SES-001 — durable Session creation

Implemented 2026-09-27.

`POST /v1/sessions` creates a tenant-owned `PROVISIONING` Session at state
version/execution generation zero and returns HTTP 201 with `Location`.
The response includes `authority_mode: local` and
`authority_issuer: thinkpixelar/local`. These identify the runtime binding;
creating a Session does not issue an ExecutionGrant or authorize compute.

## Composition and current limits

Supply `http.Options.Sessions` with `session.NewCreator` and
`http.Options.AuthenticateSession` with a trusted credential verifier. The
verifier maps tenant and issuer/subject/delegation digest; the required `Access`
callback checks current tenant status and create/disclosure policy. Caller JSON
and identity headers cannot supply these values. Replay checks both the current
request policy and access to the persisted Session ID.

API-001/API-002 still own executable authentication configuration. The stock
binary registers the route but returns 401 until authentication is composed;
there is no implicit development authentication. A verifier without a creator
returns 503. The HTTP integration test supplies an explicit test verifier.

`sessionruntimes.NewLocal` snapshots operator-approved manifests and validated
Runtime Profiles. It validates the embedded manifest schema, canonical digest,
image reference/digest equality, profile selection, isolation/network/platform
requirements and durable paths. Its mandatory trusted qualification callback
checks installed adapter/protocol/capability compatibility, image provenance and
platform qualification, returning bounded non-secret evidence. Schema validation
alone is not qualification. Dynamic marketplace and AG resolution remain future
integration work; there is no fallback between authority modes.

Only `source: {"kind":"empty"}` is implemented. Other kinds return 422 before
reservation. Workspace materialization, transition to READY, Execution creation,
and SSE remain their respective TODO tasks. The returned Workspace ID is a
reserved identity, not a claim that a volume exists.

## Transaction and replay

One PostgreSQL transaction reserves the scoped key, persists the immutable
Session binding, appends `session.created`, writes a `session.provision.v1`
outbox intent and stores the exact creation response. Intent includes reserved
Workspace/source operation IDs, request references, principal/policy identity,
resolution timestamp and implementation/qualification evidence. A provisioning
consumer must reconcile those exact IDs and persist bindings before acknowledging
that message. No external materialization occurs inside the transaction.

Keys are hashed and scoped by tenant, principal and action. JSON formatting is
irrelevant; duplicate/unknown/case-aliased fields and null substitutions are
rejected. Identical retries return the original response with
`Idempotency-Replayed: true`, even after application/catalog replacement.
Changed requests under the same scope/key return 409. Generic expiry cleanup
retains Session-creation records until a resource/tombstone-aware retention
workflow exists, preventing time-based duplicate Session/Workspace creation.

Migration 23 permits aggregate version zero only for the first
`session.created` event without Execution/Attempt lineage. The event schema and
OpenAPI now match the accepted Session state machine. The manifest schema's NUL
regex escape uses equivalent `\x00` spelling supported by the Go validator.

## Verification

- Focused race tests and `go vet` for the changed HTTP, application, resolution,
  event and persistence paths; command-package compilation and OpenAPI tests.
- Real HTTP listener and PostgreSQL, with ten concurrent identical creates,
  canonical replay, conflicts, tenant isolation, replay authorization revocation,
  catalog replacement, expiry retention and injected outbox failure rollback.
- Migrations applied to an isolated test database, not the development database.
- Pinned Redocly lint/bundle and generated artifact comparison.

Reproduce the integration check against a disposable migrated database:

```sh
THINKPIXELAR_TEST_DATABASE_URL="$TEST_DATABASE_URL" \
  go test -race ./internal/adapters/http -run TestPostgresCreateSessionHTTP -count=1
```

This proves durable HTTP creation with a test authentication/qualification
boundary. It does not claim a production identity provider, Workspace, harness,
model or governed tool execution.
