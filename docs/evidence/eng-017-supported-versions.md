# ENG-017 supported version validation

Validated 2026-09-01 on `linux/amd64`.

The repository now separates component pins exercised by Phase 1 from the
Kubernetes/Kata/CSI candidate tuple that still requires later cluster
qualification. `docs/supported-versions.md` records the scope of each tested
claim and links the existing real-service and image-smoke evidence.

The reproducible `make versions-check` gate:

- requires the running Go version to equal both `.go-version` and the `go.mod`
  directive;
- requires running Node.js and npm versions to equal `package.json` engine pins;
- verifies both service Dockerfiles use the reviewed builder and Distroless OCI
  index digests;
- verifies Compose uses the reviewed PostgreSQL OCI index digest; and
- requires the supported-version table to contain every exact tested pin.

Observed tool output:

```text
go version go1.26.7 linux/amd64
v24.11.1
11.6.2
supported versions: passed (Go 1.26.7, Node.js 24.11.1, npm 11.6.2)
```

The Docker artifacts were not reclassified as production- or cluster-qualified.
Their tested claims remain limited to the real development-service and hardened
image smoke checks linked from the supported-version table.
