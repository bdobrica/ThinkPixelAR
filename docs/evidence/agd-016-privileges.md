# AGD-016 sandbox-local privilege evidence

Date: 2026-09-22

## Scope

Extends the existing agentd image smoke target with a test-only static privilege
probe. Implements regression checks for accepted
[ADR-0013](../adr/0013-effective-sandbox-security.md) and
[ADR-0023](../adr/0023-agentd-readonly-bootstrap.md); no new architectural decision,
production code, dependency or contract change.

The real agentd runs as PID 1 in its pinned distroless image. A read-only mounted
probe executes with the same configured security restrictions and checks:

- PID-1 command identity and UID/GID 65532 for both supervisor and probe;
- empty inheritable, permitted, effective, bounding and ambient capability sets;
- no-new-privileges and active seccomp filtering;
- read-only root and bootstrap mounts;
- absent/inaccessible conventional Kubernetes credentials, runtime sockets and
  host virtualization/memory/disk devices;
- permission denial for changing to UID 0, opening an IPv4 raw socket and mounting
  tmpfs. Unexpected success fails, with cleanup of a socket/mount if created.

Docker inspection independently verifies non-privileged mode, private IPC, no
host PID namespace and disabled networking. Startup rejects an empty KUBECONFIG
override and even an empty conventional service-account projection. Existing
missing/writable-bootstrap and graceful SIGTERM cases remain in the same target.
Parser regressions reject each missing/unsafe security-status field.

## Verification

- Probe race tests: passed.
- `make agentd-image-smoke`: passed on Docker 29.0.1, Linux/amd64.
- `make verify`: passed.

## Limits

This run uses local Linux/amd64 Docker, not Kata or a Kubernetes deployment. It
qualifies the supervisor-only image in the tested restrictions, not an arbitrary
image/default Docker invocation. The probe does not establish absence of concealed
credentials or arbitrary host mounts. Network-disabled Docker is not evidence of
Kubernetes API egress policy enforcement. Existing Phase 3 trusted provider and
ARM64 Kata/network evidence remains applicable only to its qualified artifacts.

The sandbox cannot attest its own trust boundary. External observed-Pod and
infrastructure checks remain mandatory; AGD-020/final vendor-image composition
must retain these regressions and qualify the actual deployed artifact. No paid
infrastructure or new production qualification gate is introduced.
