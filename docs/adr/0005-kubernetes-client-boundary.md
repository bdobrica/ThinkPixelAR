# ADR-0005: Keep Kubernetes connections inside infrastructure adapters

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

The SandboxProvider and WorkspaceProvider require Kubernetes API access without
making Kubernetes a domain dependency. Cluster credentials belong exclusively
to the trusted control plane.

## Decision

Use the official client-go dynamic client behind an adapter-local client port.
Pin client-go and apimachinery to `v0.36.2`, matching the initial Kubernetes
qualification candidate. Application code continues to use neutral provider
ports. No Kubernetes connection or manifest enters a Session or agent process.

Connection configuration selects either in-cluster identity or an explicit
trusted development kubeconfig and context. There is no credential fallback.
Require verified HTTPS, a namespace, a positive HTTP request timeout no longer
than one minute, and bounded client request rate. Default timeout is 15 seconds.
Construction errors are sanitized before returning to the caller.

## Alternatives considered

Handwritten kubeconfig, TLS and authentication handling would duplicate a
security-sensitive upstream implementation. Sharing Kubernetes clients with
application services would violate the provider boundary.

## Consequences

Official authentication and transport support adds transitive modules, recorded
in the dependency inventory. Client construction is separate from service
startup until the provider composition is implemented; HTTP-only mode remains
usable without cluster credentials.

## Security

Development kubeconfig is trusted executable operator input, including any
credential plugins it configures. It must never originate from a caller or
Workspace. Sandbox permissions and effective-state verification remain separate
provider responsibilities; a connection does not qualify isolation.

## Operations

Requests use caller contexts as well as the configured HTTP timeout. No API
request is made during client construction. Namespace authorization remains a
cluster RBAC responsibility. Connectivity failure never selects another cluster.

## Compatibility

The dependency pin is source compatibility, not live-cluster qualification.
Public contracts are unchanged. Subsequent adapter API selection and runtime
qualification remain KAS-002 and later Phase 3 work.

## References

- [SandboxProvider](../contracts/sandbox-provider.md)
- [Supported versions](../supported-versions.md)
- [Kubernetes configuration](../kubernetes-configuration.md)
