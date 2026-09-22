# AGD-005 checkpoint — protected credential loading

Implemented 2026-09-22 under [ADR-0029](../adr/0029-agentd-bootstrap-credential-loading.md).

## Verification

- Focused agentd/transport race tests validate complete bundles, copied config and
  handshake fields, required frame Check, redacted formatting and clearing of
  owned buffers. Destroyed material cannot produce another client configuration.
- Missing/extra/oversized files, wrong proof/challenge size, invalid key/certificate,
  cross-binding identity, wrong trust domain, shared-role CA, trailing trust data,
  excessive CA count and a twelve-minute session credential used as bootstrap
  fail closed.
- Filesystem tests reject writable mounts, FIFO files and `..data` escape.
- Actual read-only container tests pass for direct and projected layouts. The
  projected case deliberately has a broken public `config.json` symlink: loading
  still uses the single pinned `..data` generation. The helper runs as UID/GID
  65532, with read-only root/bind mounts, no network, all capabilities dropped and
  `no-new-privileges`.

The helper was freshly compiled from current source, with `CGO_ENABLED=0`. It ran
in the existing local `thinkpixel-agentd:development` image
`sha256:f8f436770fad8bca6b706f856115831e803b8d780ed2295da4a7547aa5f8efa2`, overriding
the entrypoint with the test binary. This verifies the loader's filesystem behavior,
not a rebuilt release image or enabled transport. Keys/certificates are generated
in memory and temporary fixtures are removed by the tests; none are committed.

Reproduction:

```sh
go test -race ./internal/app/agentd ./internal/adapters/sandboxtransport/grpc
CGO_ENABLED=0 go test -c -o /tmp/thinkpixel-agentd-loader.test ./internal/app/agentd
THINKPIXEL_LOADER_TEST_BINARY=/tmp/thinkpixel-agentd-loader.test \
THINKPIXEL_LOADER_TEST_IMAGE=thinkpixel-agentd:development \
  go test ./internal/app/agentd -run '^TestTransportReadonlyContainer$' -count=1 -v
make verify
```

These environment variables are test-only; production has no bootstrap path,
credential or security-check override. The full `make verify` gate passed, including
generated-artifact checks, static analysis, unit/race tests, vulnerability and
dependency checks, builds and OpenAPI verification. Staged repository hygiene and
changed Markdown local-link checks also passed.
No cluster modification, paid infrastructure, new dependency,
network handshake or harness launch is claimed.
