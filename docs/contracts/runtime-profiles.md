# Runtime Profiles

Status: Normative Phase 0 contract.

The machine-readable operator contract is [Runtime Profile JSON Schema v1](runtime-profile.schema.json). The initial profile instance is [`coding-medium-secure`](../profiles/coding-medium-secure.json).

A Runtime Profile is an operator-controlled, abstract bundle of enforced compute, storage, network, platform, lifecycle, and isolation constraints. Users and AgentRuntimeSpecs refer to its stable name and abstract capabilities; they do not submit Pods, Kubernetes Agent Sandbox resources, RuntimeClass names, node selectors, storage classes, or network-policy YAML.

## Isolation classes

Isolation classes are ordered minimum guarantees:

```text
container-standard < microvm-strong < confidential-strong
```

| Class | Minimum boundary | Intended use | RC status |
| --- | --- | --- | --- |
| `container-standard` | Dedicated container namespaces/cgroups plus hardened workload configuration. Shares host kernel. | Lower-risk trusted/controlled workloads where policy accepts shared-kernel risk. | Defined, not acceptable for arbitrary hostile coding agents in the secure profile. |
| `microvm-strong` | Each Sandbox workload is isolated by a hardware-virtualized guest boundary through a qualified secure runtime; host runtime remains outside workload reach. | Untrusted harness, repository, dependencies, generated code, and shell execution. | Required and initially mapped to qualified Kata/QEMU. |
| `confidential-strong` | `microvm-strong` plus qualified confidential-compute hardware, measured boot/attestation, key release, and evidence policy. | Future workloads requiring protection from parts of host/operator plane. | Reserved, unsupported until a separate implementation/attestation contract is qualified. |

An implementation may satisfy a lower request with a higher qualified class only if platform compatibility, performance/resource policy, and authority permit it; it may never downgrade. Class names express a contract, not a proof by branding. Effective runtime tests must qualify every mapping.

## Schema sections

### Resources

CPU uses integer millicores. Memory, ephemeral storage, and Workspace capacity use integer bytes. Each runtime resource has request and hard limit; application validation enforces `request <= limit`. `max_processes` is externally enforced where supported and backed by resource/cgroup policy. GPU count/classes must agree with `platform.gpu_allowed` and are zero/empty for non-GPU profiles.

Authority may narrow limits below profile maxima only when they remain at or above AgentRuntimeSpec minimum requirements and profile requests; otherwise admission fails. Sandbox inputs cannot raise them.

### Storage

The schema fixes `/workspace` and `/state` roots and states capacity, access mode, snapshot requirement, and encryption requirement. Implementation references resolve to operator-selected CSI configuration described by the supported-version matrix. Storage details remain outside public contracts. `single-writer` is always paired with AR's database fence; `single-pod-writer` requests `ReadWriteOncePod` defense in depth where qualified.

### Network

The closed profile vocabulary is `none`, `thinkpixel-only`, `restricted-development`, `package-mirrors`, and `unrestricted-standalone`. Network enforcement is outside the untrusted workload. Cloud metadata and Kubernetes API denial are independent required controls, not assumed from default-deny alone. `unrestricted-standalone` is never valid in integrated secure profiles and must be visibly operator-selected.

### Platform

The abstract OS/architecture/node/device requirements are resolved against qualified cluster capability. `node_class` is an operator vocabulary, not a user node selector. A profile does not expose concrete topology, labels, runtime handler, or device resource names.

### Security

Security fields are explicit so an implementation cannot interpret omission as permissive. Profiles declare privilege, non-root, root filesystem, privilege escalation, service-account token, host namespace/path/socket, added-capability, and seccomp requirements. Application cross-field rules define which combinations may claim each isolation class.

### Lifecycle

Idle suspend is an AR policy trigger, not authority extension. `release-and-restore` always permits logical suspension through durable checkpoint and replacement compute. `provider-suspend-preferred` may use a qualified provider capability but must fall back safely without changing Session identity. Warm-pool eligibility is false until residue/concurrency security tests pass.

### Implementation references

`implementation` is trusted operator configuration:

- `provider_kind` selects a registered SandboxProvider;
- `provider_profile_ref` selects adapter-local template/mapping;
- `isolation_runtime_ref` selects an operator mapping to a concrete qualified runtime handler;
- `storage_profile_ref` selects qualified Workspace/snapshot policy;
- `network_policy_ref` selects externally enforced network implementation.

References are opaque stable identifiers. Their resolved content and digest are stored in each Execution's immutable resolution snapshot. Public API returns abstract/safe profile fields, not infrastructure manifests or sensitive topology.

## Validation and resolution

At startup/reload AR validates the complete profile set:

1. strict JSON/schema, duplicate-key and unknown-field rejection;
2. unique stable names and existing implementation references;
3. request/limit and GPU cross-field consistency;
4. isolation-class security invariants;
5. network/security compatibility;
6. storage/snapshot capabilities against selected provider;
7. architecture/runtime/provider capabilities against the supported-version tuple;
8. lifecycle support/fallback behavior; and
9. canonical RFC 8785 serialization and SHA-256 profile/config-reference digests.

Invalid configuration fails startup/reload atomically; the previous valid set remains active on failed hot reload. No partially parsed profile is admitted.

For an Execution, resolution computes the intersection of:

- immutable AgentRuntimeSpec minimums;
- requested profile name;
- RunAuthority resource/network/capability ceilings;
- operator profile constraints;
- current qualified provider/platform capacity and capability.

The result must be no more permissive than every authority/policy input and must satisfy every runtime minimum. AR persists the complete abstract resolved values, implementation reference/version/digest, supported-version tuple, and decision reason before Sandbox acquisition. Later operator changes cannot rewrite or widen the snapshot.

## Secure-class invariants

A profile claiming `microvm-strong` or `confidential-strong` MUST require:

- `privileged=false`, `run_as_non_root=true`, `allow_privilege_escalation=false`;
- no service-account token, host network/PID/IPC, hostPath, runtime socket, or added Linux capability;
- read-only root filesystem unless an explicitly reviewed adapter compatibility exception supplies bounded ephemeral writable mounts;
- qualified seccomp policy, resource requests/limits, process bound, and ephemeral-storage bound;
- cloud metadata and Kubernetes API denial;
- default-deny ingress/egress with an approved network profile;
- immutable image digest and qualified isolation runtime mapping;
- only bounded Workspace/vendor/ephemeral mounts;
- effective-state inspection and hostile runtime probes.

An exception cannot continue to advertise the class unless the class contract explicitly allows it through a reviewed new schema/ADR. Operator mapping by name is not proof.

## Change and compatibility policy

Profiles are versioned configuration artifacts. Changing values/reference targets creates a new canonical digest. New Executions may use the new resolution; existing Execution snapshots remain unchanged. A material change that could weaken isolation, networking, storage durability, or resource enforcement requires security review and qualification. Removing a profile requires inventory and handling for bound Sessions; it cannot cause automatic substitution.

The API/schema `schema_version` changes only for semantic or structural incompatibility. Unknown versions fail closed. Adding a new isolation class requires an ADR, threat-model update, provider capability vocabulary, and independent conformance/security evidence.

## Verification requirements

- Draft 2020-12 schema validation plus golden valid profiles and rejection of every missing/unknown/invalid field.
- Cross-field tests for requests/limits, GPU, isolation/security, network mode, storage/snapshot, architecture, and lifecycle.
- Deterministic canonical digest tests and atomic reload tests.
- Authority intersection tests proving no resource/network/capability widening.
- Adapter mapping tests proving no raw infrastructure type enters domain/public contracts.
- Effective-state/adversarial tests proving the secure profile receives its claimed runtime, mounts, credentials, namespaces, capabilities, resources, and network denials.

