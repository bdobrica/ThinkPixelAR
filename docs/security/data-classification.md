# Data classification and redaction

Status: Normative Phase 0 security contract.

## Purpose

This contract defines how ThinkPixelAR classifies, stores, transmits, logs, and redacts runtime data. It applies to API payloads, PostgreSQL, events, logs, metrics, traces, Workspace and vendor state, checkpoints, artifacts, backups, support bundles, and verification evidence.

Data keeps the highest classification contributed by its source or content. Encoding, hashing, tokenization, compression, encryption, truncation, or moving data into an identifier field does not lower its classification. When classification is unknown, treat the value as `Restricted` until an explicit policy proves otherwise.

## Classification levels

| Level | Meaning | Examples | Baseline handling |
| --- | --- | --- | --- |
| `Public` | Approved for public disclosure. | Published documentation, public API schemas, explicitly public repository content. | May appear in any approved sink. Integrity controls still apply. |
| `Internal` | Operational metadata that is not intended for public disclosure and contains no tenant content or credential material. | Service version, bounded status enum, Runtime Profile name, adapter kind, coarse timing, opaque AR IDs. | Authenticated access; may appear in structured logs, metrics dimensions only when bounded, events, and traces. |
| `Confidential` | Tenant/user content or business data whose disclosure is harmful. | Prompts, model output, private repository content, Workspace files, vendor conversation state, tool input/output, checkpoint and artifact contents. | Encrypt in transit and at rest using deployment controls; tenant-scoped authorization; minimize collection; no logs, metrics, or trace attributes by default. |
| `Restricted` | Security-sensitive data whose misuse can grant authority, defeat controls, or expose protected internals. | Credentials, tokens, cookies, private keys, authorization headers, sandbox bootstrap material, raw OIDC claims, sensitive configuration, unredacted dumps. | Never persist except in an explicitly designed secret store; never emit to logs/events/traces/artifacts/checkpoints; shortest possible memory lifetime; fail closed on handling uncertainty. |

Regulatory or operator labels MAY impose stronger handling than this baseline. They MUST NOT weaken it.

## Data-category rules

| Category | Default class | Durable authoritative storage | Runtime Events | Logs / traces / metrics | Checkpoints / artifacts | Required treatment |
| --- | --- | --- | --- | --- | --- | --- |
| Prompts and user input | Confidential | Only when required for the product contract and with tenant scope/retention | User-visible payload or protected reference when explicitly required | Metadata only; no content | Only by explicit artifact/checkpoint contract, never incidentally | Bound size; authorize reads; redact detected credentials; honor deletion/retention policy. |
| Model output | Confidential | User-visible output/events or protected artifact reference | Allowed when it is intended user-visible output | Metadata only; no content | Explicit publication only | Do not persist hidden chain-of-thought; classify embedded secrets or source excerpts at their higher level. |
| Repository content | Confidential unless proven Public | Workspace/object storage, not AR metadata rows | References and bounded safe summaries only | Paths only when safely normalized and policy permits; no file contents/diffs | Allowed as access-controlled Workspace snapshots or explicit artifacts | Preserve tenant/source authorization; prevent path traversal; avoid public evidence bundles. |
| Vendor state | Confidential | Durable vendor-state storage required for resume | Opaque status/reference only | Adapter kind/version and bounded status only | Allowed only in checkpoint-designated paths | Exclude transient credentials, caches containing secrets, sockets, process state, and unrelated home-directory data. |
| Workspace contents | Confidential | Session-scoped durable storage | References and lifecycle metadata only | Volume/generation/byte counts only | Checkpoint/snapshot allowed; artifacts require explicit selection | Enforce Session binding, mount boundary, retention, integrity, and secret scanning where published. |
| Credentials and secret material | Restricted | Only an approved external secret store or narrowly defined encrypted authority record; never general runtime payload storage | Forbidden | Forbidden; metric labels forbidden | Forbidden | Redact recursively; never copy into Workspace/vendor paths; expire/revoke and clear execution injection on process end. |
| Runtime Event envelope metadata | Internal | Append-only event store | Allowed | Selected bounded correlation fields allowed | Not copied by default | Use opaque IDs; tenant-authorize stream; bound type, size, sequence, and retention. |
| Runtime Event payload | Confidential by default | Store only registered schema or protected reference | Allowed only to authorized stream consumers | Forbidden except allowlisted metadata | Not copied by default | Per-event schema defines fields; reject unknown sensitive fields; no chain-of-thought. |
| Logs | Internal sink; values independently classified | Approved log backend | Not applicable | Internal allowlist only | Excluded | Structured fields; recursive redaction before serialization; bounded error strings; no arbitrary object dumps. |
| Traces | Internal sink; values independently classified | Approved trace backend | Not applicable | Names/status/opaque IDs and coarse sizes only | Excluded | No request/response bodies, prompts, output, SQL values, headers, environment, or token-bearing URLs. |
| Metrics | Public-to-Internal aggregate sink | Metrics backend | Not applicable | Low-cardinality allowlist only | Excluded | Never use tenant, Session, Execution, Attempt, sandbox, user, path, prompt, error text, or token as labels. |
| Checkpoints | Confidential container | Integrity-bound tenant storage plus metadata reference | Lifecycle metadata/reference only | Opaque ID, generation, size, result | Checkpoint itself | Manifest allowlist; exclude credentials; bind Session, generation, adapter/version, digest, and creation context. |
| Artifacts | Confidential by default | Access-controlled artifact storage | Metadata/reference only | Opaque ID, media class, size, result | Artifact itself | Explicit publication; declared media type; size limit; integrity digest; retention; optional malware/secret/content scan. |

## Prohibited content

The following MUST NOT be written to logs, traces, metrics labels, general Runtime Event metadata, checkpoints, artifacts, support bundles, test snapshots, or evidence:

- passwords, API keys, bearer tokens, session cookies, refresh tokens, private keys, client secrets, signed URLs with live credentials, or complete authorization headers;
- Kubernetes service-account tokens, kubeconfigs, cloud metadata credentials, registry pull credentials, database DSNs containing passwords, or sandbox bootstrap authentication material;
- process environments, HTTP headers, arbitrary configuration objects, or command lines before field-level classification and redaction;
- hidden model chain-of-thought, reasoning tokens, or vendor-private reasoning fields.

User-visible model explanations and summaries are ordinary model output, not hidden chain-of-thought, and remain `Confidential` by default.

## Redaction contract

Redaction occurs before serialization and before a value crosses into a less restricted sink.

1. Build records from an allowlisted schema; do not serialize arbitrary request, error, configuration, protocol, or vendor objects.
2. Recursively inspect maps, structures, arrays, nested errors, and URL/query/header fields.
3. Replace known sensitive values with the literal `[REDACTED]`; omit a field entirely when its presence is itself sensitive.
4. Treat field names matching credential semantics case-insensitively, including `authorization`, `token`, `secret`, `password`, `cookie`, `private_key`, `credential`, and provider-specific equivalents, as `Restricted`.
5. Maintain exact-value redaction for secrets injected or loaded by the process so an unusual field name cannot bypass redaction.
6. Apply bounded pattern detection for common token/key formats as defense in depth. Pattern matching is not the primary secret boundary and false negatives MUST be assumed.
7. Sanitize errors at the boundary where they are created. Wrapped errors MUST NOT reintroduce an unsafe underlying value.
8. Do not hash secrets for correlation. Use independently generated opaque identifiers.
9. If safe redaction cannot be guaranteed, drop the field or event and emit a non-sensitive redaction-failure counter.

Redaction is not authorization. Authorized content endpoints and event streams still enforce tenant and object access before returning unredacted `Confidential` data.

## Approved correlation fields

Structured logs and traces MAY use the following after validation and cardinality controls:

- request and W3C trace identifiers;
- opaque tenant, Session, Execution, Attempt, and Sandbox identifiers in logs/traces, but not metrics labels;
- registered event type, adapter kind, Runtime Profile name, state enum, status code, result class, and bounded duration/size;
- external Run identifier only where the integration contract classifies it as no higher than `Internal`; otherwise use an AR-generated correlation reference.

Free-form names, repository paths, branch names, user names, prompt fragments, model output, tool arguments/results, and raw vendor errors are not approved correlation fields.

## Sink behavior

### Runtime Events

Every event type MUST have a registered payload schema and classification. The envelope and payload have independent size limits. Large or sensitive content uses an authorized artifact reference. SSE cursor resume MUST NOT bypass current authorization or retention. Event payloads are not copied into logs when publishing or delivering them.

### Logs and traces

Production defaults record lifecycle transitions and safe metadata, not content. Debug mode does not relax classification. Panic, retry, protocol-decoding, HTTP-client, database, and reconciliation paths use the same redaction pipeline as normal requests.

### Checkpoints and artifacts

Publication is explicit and atomic only after integrity metadata is durable. A checkpoint uses an allowlisted manifest and path set. Artifact creators declare content class and media type; consumers authorize the referenced tenant/object rather than trusting a URL. Signed download URLs are short-lived and MUST be redacted from telemetry.

## Retention, deletion, and access

- Retention is configured per data category and deployment policy, with the shortest duration compatible with recovery and audit requirements.
- Closing or deleting a Session schedules data cleanup subject to documented legal/operational retention; it does not silently leave reusable credentials.
- Backups inherit the highest classification of their contents and require access control, encryption, expiry, restore testing, and audited deletion.
- Support and development access is authenticated, authorized, time-bounded, and audited. Production content is not copied into development fixtures.
- Data exports preserve classification metadata and tenant boundaries.

## Verification requirements

- Seed unique canary secrets in prompts, nested errors, environment variables, headers, vendor events, tool results, Workspace files, and checkpoint candidate paths.
- Assert no canary appears in logs, traces, metrics exposition, event metadata, panic output, checkpoints, artifacts, support bundles, or evidence unless that sink is explicitly authorized for that exact content category.
- Test mixed-case and nested sensitive keys, arrays, URL credentials/query parameters, multiline values, malformed objects, oversized values, and wrapped errors.
- Verify metrics label cardinality and allowlists.
- Inspect checkpoint manifests and restored files to prove execution-scoped credentials are absent.
- Verify unauthorized and cross-tenant reads fail without revealing object existence.

## Change control

A new event type, telemetry field, persistent path, checkpoint member, artifact type, credential, integration, debug endpoint, or support export MUST receive a classification and sink review before release. Changing a category to a lower classification requires a security decision record and migration/retention analysis.

