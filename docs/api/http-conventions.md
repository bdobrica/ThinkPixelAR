# HTTP API conventions

Status: Normative Phase 0 contract.

## Authentication and authorization

TLS is mandatory except loopback-only development. Bearer/OAuth credentials are accepted only from the configured `Authorization` header and verified for issuer, signature/key, audience, time, revocation/security epoch, subject/actor/delegation, and allowed algorithm. Tenant and principal come from verified identity/server bindings, never JSON, query parameters, forwarding headers, paths, or sandbox claims.

Every lookup/mutation authorizes action plus tenant/resource ownership without revealing existence. User APIs return `404` for absent or non-visible IDs where enumeration resistance requires it. Administrative routes require a separate explicit audience/scope/role, are audited, bounded, and never become a generic database/provider proxy. Health endpoints expose only coarse status and no dependency/config/tenant details.

## Correlation and tracing

Clients may send `X-Request-ID` only in the documented bounded syntax; AR validates it or creates UUIDv7, returns `X-Request-ID`, and uses it for correlation—not identity/idempotency. Mutations require `Idempotency-Key` as defined by [idempotency.md](../contracts/idempotency.md); 16–128 visible URL-safe characters, never logged raw, and response includes `Idempotency-Replayed: true|false`.

AR extracts W3C `traceparent` and optional bounded `tracestate` using W3C validation, creates a server span, and propagates a newly generated/sanitized context to trusted dependencies. Invalid context is discarded/rejected per endpoint policy, never treated as identity. Baggage is deny-by-default; prompts, content, credentials, tenant/user names, tokens, raw IDs with sensitive meaning, and arbitrary client baggage do not enter spans. Responses may include `traceparent`; problems include request ID, not internal stack/span data.

## Pagination

Collections use `limit` (default 50, maximum 200) and opaque `cursor`. Stable ordering is documented per resource (normally `(created_at,id)`; events use Session sequence). The cursor is authenticated/encrypted or MAC-signed and binds tenant, principal visibility/security epoch, route/API version, filters/sort, last key/sequence, page size ceiling, snapshot/retention facts where needed, and expiry.

Cursors are not user-editable database offsets and confer no authorization. Invalid/tampered/expired/wrong-context cursors yield a stable `invalid-cursor` problem. Deletion/concurrent insertion cannot produce cross-tenant rows; duplicates around a changing collection are tolerated/documented. Event history outside retention yields `410 event-history-gone` with bounded earliest/latest sequence.

## RFC 7807 problems

Errors use `application/problem+json` with absolute stable `type`, bounded `title`, numeric `status`, optional sanitized `detail`, request-scoped `instance`, stable `code`, and `request_id`. Validation may include bounded field errors using JSON Pointer; it never echoes secrets, authorization, full bodies, provider payloads, SQL, stack traces, host paths, or existence-sensitive detail.

Core codes/statuses include:

| Status | Codes |
| --- | --- |
| 400 | `invalid-request`, `invalid-cursor`, `unsupported-signal` |
| 401 | `authentication-required`, `invalid-credential` |
| 403 | `permission-denied`, `profile-not-allowed` |
| 404 | `resource-not-found` |
| 409 | `state-conflict`, `version-conflict`, `idempotency-key-conflict`, `single-writer-conflict` |
| 410 | `event-history-gone` |
| 413/414/431 | `body-too-large`, `uri-too-long`, `headers-too-large` |
| 415/422 | `unsupported-media-type`, `semantic-validation-failed` |
| 429 | `rate-limited`, with bounded `Retry-After` |
| 501 | `capability-unavailable` (known deployment capability, such as fork) |
| 503/504 | `temporarily-unavailable`, `dependency-timeout` |

Internal/provider errors map once at the HTTP boundary. Retryability and ambiguous operation references are explicit; a `5xx` never implies a mutation did not commit.

## Request limits and parsing

The server enforces limits before allocation/decoding: request line 8 KiB, total headers 32 KiB and 100 fields, individual header 8 KiB, JSON body 256 KiB by default (endpoint-specific smaller limits; explicitly documented artifact/reference endpoints may differ), JSON nesting 32, object members 1,024, arrays 10,000, and strings according to schema. Compressed request bodies are disabled in Phase 0 to avoid expansion ambiguity.

Only declared media types/UTF-8 are accepted. JSON rejects duplicate keys, trailing data, invalid Unicode/control encodings, nonfinite/out-of-range numbers, unknown fields in closed schemas, and content after the bounded body. Multipart/arbitrary uploads are absent from the initial API. Server read-header/read/body/idle/write timeouts and per-principal/tenant/IP concurrency/rate budgets fail early without unbounded queues.

## Concurrency and asynchronous operations

State-sensitive mutations require quoted numeric `If-Match` representing `state_version`; mismatch is `409 version-conflict`. `Idempotency-Key` handles transport replay, while optimistic version and authority/fence decide mutation permission. Long operations return `202`, `Location: /v1/operations/{id}`, and bounded `Retry-After`. Clients poll the same operation; they do not resubmit with new keys after ambiguous responses.

## SSE events

SSE uses `Content-Type: text/event-stream`, UTF-8, `Cache-Control: no-cache`, proxy buffering disabled, and authenticated tenant/Session access for the full connection. Each durable event frame has `id` equal to decimal Session sequence, `event` equal to registered type, and `data` containing one bounded JSON RuntimeEvent. Heartbeat comments contain no data and do not advance sequence.

Resume uses validated `Last-Event-ID` (or an authenticated cursor for the initial request, never both inconsistently). AR sends events strictly after that sequence, then transitions to live delivery without a race. Reconnect is at least once; clients deduplicate by event ID/sequence. If the requested sequence predates retention, AR returns `410` before starting SSE with earliest/latest facts; it never silently starts at “now.”

Each connection has bounded pending events/bytes (initial target 1,000 events or 4 MiB, whichever first), write deadline, lifetime and heartbeat. Durable events remain in PostgreSQL; a slow consumer is disconnected with an observable reason/retry hint rather than consuming unbounded memory or silently dropping frames. Clients resume from their last fully received ID. Connection counts are limited by tenant/principal/IP and global capacity. Cancellation/disconnect stops stream work but does not mutate the Session.

## Security headers and caching

Authenticated/resource/problem responses use `Cache-Control: no-store` unless a public immutable capability document explicitly says otherwise, `X-Content-Type-Options: nosniff`, and no reflective CORS by default. Browser CORS origins/methods/headers/credentials are an explicit deployment allowlist. Redirects are not used for mutation/auth flows; resource locations are same-origin relative references.

## Verification requirements

- Issuer/audience/tenant/subject/delegation/revocation, JSON/header identity injection, enumeration, admin/user separation, and health disclosure tests.
- Cursor tamper/expiry/filter/principal/tenant/security-epoch substitution plus concurrent collection changes.
- Every problem mapping, redaction/duplicate-key/invalid encoding, limits/timeouts/rate/concurrency and ambiguous mutation tests.
- W3C valid/invalid trace context, baggage denial, propagation and telemetry secret-canary tests.
- SSE ordered catch-up/live handoff, duplicate reconnect, `Last-Event-ID`, retention gap, slow reader, write failure, heartbeat, connection quotas, and failover tests.
