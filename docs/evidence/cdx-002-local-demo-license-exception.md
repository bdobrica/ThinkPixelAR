# CDX-002 local demo packaging exception

Historical record: retired by [ADR-0049](../adr/0049-use-based-dependency-licensing.md)
on 2026-09-23. The local-only restriction and expiry below no longer apply and
the calendar gate has been removed. The original approval is preserved below;
source/notice obligations now follow the current [dependency policy](../security/dependency-policy.md).
This record does not establish image redistribution compliance.

Approval: repository owner/user, 2026-09-23, explicitly approved the local-demo
exception requested for Git, Bash and Alpine BusyBox as separate executables.
Owner: ThinkPixelAR maintainer. This records that approval; it is not a claim of
independent legal review or permission to distribute an image publicly.

## Scope and expiry

Applies only to the locally built Codex demo runtime in `Dockerfile.codex`:

| Dependency | Exact selection | License / use |
| --- | --- | --- |
| Git | Alpine `2.52.0-r0` | GPL-2.0-only; separate repository-operation executable |
| Bash | Alpine `5.3.3-r1` | GPL-3.0-or-later; separate shell executable |
| BusyBox | Alpine `1.37.0-r30`, base index `sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0` | GPL-2.0-only; base-image utilities |

The [resolved inventory](cdx-002-image-packages.tsv) is part of this local-only
implementation scope: the selected Alpine base and the unmodified runtime closure
of the coding tools. It includes split Git/BusyBox packages, Readline `8.3.1-r0`,
Alpine baselayout `3.7.2-r0`, apk-tools/libapk `3.0.8-r0`, musl-utils `1.2.5-r23`,
scanelf `1.3.8-r2` and libgcc `15.2.0-r2` with their recorded copyleft metadata.
These remain distro executables/data or support libraries of packaged tools;
agentd is built with CGO disabled. This interprets the owner's local-demo approval
as covering that package closure, without claiming an independent legal review.

Implementation license disposition for the other recorded runtime dependencies:
select MIT for ripgrep, BSD-3-Clause for zstd, and LGPL-3.0-or-later for libidn2 and
libunistring where the APK metadata offers alternatives. Preserve the combined
MPL-2.0/MIT certificate-data notices and the curl, X11 and Zlib notices. These
unmodified distro components share the local-only scope and expiry; their source,
linkage and redistribution disposition require the release review described below.

No linking into AR/agentd, source modification, control-plane dependency, change
to AR's Apache-2.0 license or authority expansion is authorized. The exception
expires **before any public image distribution**, or on **2026-12-23**, whichever
comes first. Any version, base digest or redistribution-scope change requires
review. Remove these packages or replace this exception with a reviewed release
disposition before that boundary; local tags are not release approval.
The `make license` dependency gate rejects the calendar-expired exception.

## Notices, sources and controls

Preserve the image's `/lib/apk/db/installed`, plus its copied inventory under
`/usr/share/thinkpixel/`. These retain exact versions, package checksums, origins,
license identifiers and upstream project references, including transitive packages.
Do not strip license/notice files supplied by packages. Codex's upstream LICENSE
and NOTICE are separately checksum-verified and installed under
`/usr/share/licenses/codex/`.

Corresponding source/build recipes and upstream license references:

- [Alpine 3.23 Git recipe](https://gitlab.alpinelinux.org/alpine/aports/-/tree/3.23-stable/main/git),
  [Git COPYING](https://github.com/git/git/blob/v2.52.0/COPYING).
- [Alpine 3.23 Bash recipe](https://gitlab.alpinelinux.org/alpine/aports/-/tree/3.23-stable/main/bash),
  [Bash source archives](https://ftp.gnu.org/gnu/bash/),
  [GPLv3](https://www.gnu.org/licenses/gpl-3.0.html).
- [Alpine 3.23 BusyBox recipe](https://gitlab.alpinelinux.org/alpine/aports/-/tree/3.23-stable/main/busybox),
  [BusyBox source archives](https://busybox.net/downloads/),
  [GPLv2](https://www.gnu.org/licenses/old-licenses/gpl-2.0.html).

These references support local inspection; they are not a substitute for fulfilling
corresponding-source, notice or source-offer obligations for redistribution.
Before public publication, retain the exact source/patches and notices for the
distributed binaries, review the full transitive inventory and vulnerability
disposition, and satisfy the repository's release SBOM/provenance requirements.
The exception does not approve packages or features outside the recorded closure.

Sandbox controls remain unchanged: non-root agentd entrypoint, authenticated
dispatch, execution-local credentials, read-only root in tests, and the existing
Kata/network/authority policies for eventual live execution.
