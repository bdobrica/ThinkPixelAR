# ENG-013 thinkpixelar container image

Reviewed 2026-08-31.

| Dependency | Class | Exact artifact | License | Repository-local purpose |
| --- | --- | --- | --- | --- |
| Go Docker Official Image | Build-only toolchain | `golang:1.26.7-alpine3.23@sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57` | BSD-3-Clause (Go); bundled components retain their licenses | Compile the exact Go 1.26.7 module in an isolated stage. |
| Distroless static Debian 13 | Production runtime base | `gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7` | Apache-2.0; bundled components retain their licenses | Minimal runtime files, CA certificates, and a declared non-root identity without a shell or package manager. |

Both references pin multi-platform OCI index digests so a build resolves the
matching immutable platform manifest. The build uses the same exact Go patch
as `.go-version` and `go.mod`, disables CGO, strips local source paths and VCS
metadata, and copies only the service binary into the runtime stage.

The final image explicitly runs as numeric UID/GID `65532:65532`. The smoke
gate additionally runs it with a read-only root filesystem, all Linux
capabilities dropped, and `no-new-privileges`, then checks `/livez`. Those
runtime flags document a compatible hardened deployment posture; Kubernetes
manifests remain responsible for enforcing equivalent settings when added.
The image does not contain `migrate`, does not apply schema changes, and is
separate from the vendor and `thinkpixel-agentd` images owned by later work.

Sources:

- <https://hub.docker.com/_/golang>
- <https://github.com/GoogleContainerTools/distroless>
