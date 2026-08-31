# ENG-011 verification tools

Status: Engineering dependency review recorded 2026-08-31.

The root Makefile pins these analysis-only dependencies independently of the
runtime module:

| Tool | Version | License | Repository-local purpose |
| --- | --- | --- | --- |
| Staticcheck (`honnef.co/go/tools`) | `v0.8.1` | MIT | Go static analysis beyond `go vet`; supports the pinned Go language version. |
| govulncheck (`golang.org/x/vuln`) | `v1.7.0` | BSD-3-Clause | Call-graph-aware reporting against the Go vulnerability database. |

Both are build/analysis tools, not shipped runtime libraries or authority
dependencies. `go run module@version` preserves exact source versions and Go
checksum-database verification without adding tool-only libraries to the
service module. A clean tool cache and vulnerability scan require network
access. No analyzer receives credentials or runtime payloads.

Go dependency license decisions are recorded by exact module and version in
`build/dependency-licenses.tsv`; the gate fails when the packages reachable by
runtime or tests diverge from that reviewed manifest. The npm lock-file gate
likewise requires exact versions, integrity hashes, and allowed SPDX licenses
for every build dependency. This repository-owned check avoids relying on
go-licenses v2, whose package loader does not support the pinned Go 1.26
downloaded-toolchain layout.
