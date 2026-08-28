# AgentRuntimeSpec

Status: Normative Phase 0 contract.

The machine-readable contract is [AgentRuntimeSpec JSON Schema v1](agent-runtime-spec.schema.json). `AgentRuntimeSpec` describes immutable executable packaging and compatibility requirements; it is not a Pod, Sandbox, resource grant, credential bundle, or mutable operator configuration.

## Example

```json
{
  "schema_version": 1,
  "runtime_id": "codex-app-server",
  "image": {
    "reference": "registry.example/thinkpixel/codex@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "manifest_media_type": "application/vnd.oci.image.manifest.v1+json"
  },
  "adapter": {
    "kind": "codex-app-server",
    "protocol_version": "1.0.0",
    "compatibility": ">=1.0.0 <2.0.0",
    "vendor_state_format": "codex/thread-v1"
  },
  "entrypoint": {
    "command": ["/opt/thinkpixel/bin/codex-app-server"],
    "working_directory": "/workspace",
    "shutdown_grace_seconds": 30
  },
  "durable_vendor_paths": ["/state/codex"],
  "workspace_mount": "/workspace",
  "runtime_profile": {
    "name": "coding-medium-secure",
    "minimum_isolation_class": "microvm-strong",
    "required_network_profile": "thinkpixel-only",
    "required_capabilities": ["structured-events", "resume"]
  },
  "platform": {
    "os": "linux",
    "architectures": ["amd64", "arm64"],
    "minimum_agentd_protocol": "1.0.0",
    "requires_gpu": false
  },
  "declared_environment": ["THINKPIXEL_LLMGW_ENDPOINT", "THINKPIXEL_TG_ENDPOINT"]
}
```

## Field semantics

### Schema and identity

`schema_version` is exactly `1`. Unknown versions fail closed. `runtime_id` is a stable operator/governance identifier within its authority namespace; it is not sufficient identity by itself. The canonical spec digest and authority agent/version identifiers form the Session binding described by ADR-0001.

### OCI image

`image.reference` MUST contain an OCI/Docker distribution reference ending in `@sha256:<64 lowercase hex>`. Tags without a digest, uppercase/noncanonical digest text, shortened digests, and implicit registry defaults are rejected. `image.digest` MUST equal the digest suffix of `image.reference`; JSON Schema validates each shape and application validation enforces equality.

AR resolves the reference through the configured registry, verifies the returned manifest digest, platform selection, allowed media type, and supply-chain policy, then records the resolved manifest/index evidence. The image cannot request runtime privileges beyond the Runtime Profile.

### Adapter

`adapter.kind` selects a registered vendor-neutral `HarnessAdapter` implementation. `protocol_version` is the exact protocol expected from the packaged harness endpoint. `compatibility` is a bounded implementation compatibility range interpreted by the adapter registry's version-range parser, not by shell evaluation. `vendor_state_format` names the durable format when state is persisted.

Before materialization, the selected adapter must declare the same kind, support the protocol/range, satisfy required capabilities, understand the vendor-state/checkpoint format, and support the platform/`agentd` protocol. Unknown or ambiguous ranges fail closed.

### Entrypoint

`entrypoint.command` is an argv array executed directly without a shell. The first element is the executable path/name as defined by image packaging; no string concatenation or shell expansion occurs. Arguments may not contain NUL or newlines and are size/count bounded. `working_directory` is an absolute normalized in-sandbox path. `shutdown_grace_seconds` is bounded and cannot exceed platform termination policy.

The spec cannot set user IDs, privilege, mounts, devices, Kubernetes fields, seccomp, capabilities, networking, or credentials. Trusted materialization owns those controls.

### Durable vendor paths

`durable_vendor_paths` is a sorted, unique allowlist under `/state/`. Paths are absolute, lexically clean, non-overlapping, and MUST NOT be `/state` itself. Application validation rejects `.`/`..`, repeated separators, trailing separators, symlink escape, mount overlap, device/socket paths, the Workspace path, and any parent/child overlap between entries.

Only these paths are eligible for vendor-state persistence/checkpoint preparation. Declaring a path does not make its content trusted or permit credentials. Runtime-generated state elsewhere remains ephemeral.

### Workspace mount

`workspace_mount` is exactly `/workspace` in v1. The Workspace is independently provisioned, tenant/Session bound, and mounted by trusted infrastructure. The image/spec cannot substitute another volume, mount the host, or persist the sandbox root.

### Runtime Profile requirements

`runtime_profile.name` selects the requested abstract profile. `minimum_isolation_class`, optional required network profile, and required capability names are lower bounds. Operator resolution may be equally or more restrictive but cannot weaken them. The immutable per-Execution resolution snapshot is separate from this spec because resources and infrastructure mappings may be narrowed for each authorized operation.

### Platform

V1 supports Linux with `amd64` and/or `arm64`. The registry-selected image platform, node/platform placement, Kata/runtime capability, and adapter binary must agree. `minimum_agentd_protocol` is an exact minimum semantic version for the packaged runtime contract. GPU is capability-gated: `requires_gpu=true` requires at least one abstract GPU class; false forbids GPU classes. Concrete device plugin/resource names remain operator configuration.

### Declared environment

`declared_environment` lists names only for non-secret configuration the trusted materializer may supply. Unknown undeclared variables are not injected except platform-reserved variables defined by the `agentd` protocol. Values, especially secrets, are never stored in AgentRuntimeSpec. Execution-scoped credentials use a separate injection mechanism and are excluded from persistent state.

## Canonicalization and digest

Before hashing, AR:

1. validates strict JSON with duplicate keys rejected and no unknown properties;
2. applies explicit schema defaults in trusted code (`shutdown_grace_seconds=30`, `requires_gpu=false`) rather than relying on validators to mutate input;
3. normalizes and validates paths without resolving them through an untrusted filesystem;
4. sorts set-like arrays (`durable_vendor_paths`, architectures, GPU classes, required capabilities, declared environment);
5. emits RFC 8785 JSON Canonicalization Scheme bytes; and
6. computes lowercase `sha256:` over those bytes.

Semantically equivalent inputs produce the same digest. The original signed/governance artifact and canonical digest may both be retained as evidence; consumers use the validated canonical representation.

## Cross-field and security validation

JSON Schema is necessary but not sufficient. Application validation also enforces:

- reference digest equals `image.digest` and the registry response;
- identifier uniqueness and sorted canonical sets;
- clean, non-overlapping durable paths beneath `/state` and separate from `/workspace`;
- entrypoint/working directory compatibility with image policy;
- adapter kind/protocol/state-format compatibility and required capabilities;
- Runtime Profile provides at least requested isolation/network/platform/device capabilities;
- GPU field consistency and an available approved platform;
- no secret-looking environment names where policy reserves credential injection names;
- normalized total document size at most 64 KiB and bounded error output.

Validation errors use stable field/problem codes without echoing the complete manifest or potentially sensitive values.

## Immutability and resolution

An authority-owned manifest may contain this spec or an integrity-bound reference to it. Session creation resolves, validates, canonicalizes, and stores the spec/digest before external materialization. The bound canonical spec is immutable. Operator mappings such as registry mirrors, SandboxProvider, RuntimeClass, StorageClass, nodes, and concrete network implementation are not fields and are captured separately in resolution evidence.

Changing any spec field creates a different runtime spec/version and requires a new Session or future explicit migration. An adapter implementation security patch that still satisfies the bound adapter contract does not alter the spec, but its own build/version evidence is recorded per materialization.

## Compatibility and evolution

V1 readers reject an unknown `schema_version`; writers never add fields without a new schema version. A future schema defines explicit upgrade/canonicalization rules and cannot reinterpret an old digest. Removing support for a schema, architecture, adapter range, or state format requires operator inventory, deprecation notice, and handling for bound Sessions.

## Verification requirements

- Validate the schema itself with a Draft 2020-12 validator.
- Golden valid example plus rejection cases for every required/unknown field, bound, enum, identifier, digest, SemVer, path, command, architecture, and GPU conditional.
- Application cross-field tests for digest equality, canonicalization stability, duplicate JSON keys, path overlap/traversal, adapter compatibility, profile narrowing, and total size.
- Registry tests for tag substitution, manifest/index digest mismatch, unsupported media/platform, and immutable pull evidence.
- Fuzz tests for parsing/canonicalization with bounded memory/error output.
- Secret-canary tests proving manifests/spec errors and persisted snapshots never acquire execution credential values.

