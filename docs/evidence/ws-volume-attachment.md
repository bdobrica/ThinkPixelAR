# ThinkPixelWS volume attachment (WS TAR-003)

The Kubernetes `WSVolumeResolver` connects a resolved ThinkPixelWS
`api/storagebinding.Binding` to the existing AR attachment/PVC verifier and KAS
coding template. WS owns its Materialization/PVC; AR owns disposable compute.
No WS database or internal package is accessed, and no storage mutation occurs.

Compose the existing `AttachedBlueprintResolver` with
`kubernetes.NewWSVolumeResolver(pvcVerifier, lookup)` as its volume resolver.
The `AttachmentReader` must still return the current authoritative AR reservation.
The trusted `StorageBindingLookup` receives that exact reservation on every lookup
(including acquisition replay) and must resolve its saved WS handle after checking
current AG authority, WS writer lease/fence, target, audience, expiry and exact
whole-Workspace component access. Failure must deny attachment. Neither this
callback nor a binding is authorization by itself. Do not supply it from workload
input or a cached grant decision.

The reservation pins the Workspace PVC as `namespace/name/UID`, a separate
vendor-state PVC, capacity/access semantics and qualified storage profile.
The bridge checks the versioned WS descriptor, provider handle/UID consistency,
namespace, identity, mount root and explicit access mode against that reservation.
The existing verifier then checks live PVC identity, Bound/filesystem state,
requested/effective capacity, access mode and independent storage qualification.
Only verified existing claims enter the Sandbox template; no claim templates or
storage ownerReferences are created. Bootstrap remains a separate lookup.

The current coding template accepts only read-write `/workspace` attachments.
Read-only WS bindings are rejected, never upgraded to write access. Whole-PVC
mounts cannot enforce component subsets or mixed modes. A PVC name cannot pin a
UID atomically at Pod creation: trusted control-plane ownership must prevent claim
replacement between verification and mount. RWO alone does not fence writers.

Verification (2026-09-27):

```sh
go test ./internal/adapters/workspace/kubernetes ./internal/adapters/sandbox/agentsandbox
go vet ./internal/adapters/workspace/kubernetes ./internal/adapters/sandbox/agentsandbox
```

Tests cover descriptor/identity/mode rejection, verifier failure, cancellation,
replacement live PVC UID, scope propagation, KAS Sandbox creation and replay,
and denial when WS verification fails on replay. Kubernetes and WS authority
lookups use fixtures; this is adapter integration evidence, not a live governed
Session or proof of storage durability. The running WS binding endpoint, AG
verification, durable remote reservation mapping and AR service composition are
still required for a live deployment. Encryption/snapshot qualification is not
inferred from WS descriptors or the homelab local-path StorageClass.
