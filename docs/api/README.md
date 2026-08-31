# Public API

- [OpenAPI 3.1 source contract](openapi.yaml)

The build-only OpenAPI toolchain is pinned by `.node-version`, `package.json`,
and the root `package-lock.json`.
After changing the source contract, run `npm ci` and
`npm run openapi:generate`. Commit the reviewed generated
`api/openapi/openapi.json` artifact. `npm run openapi:check` validates the
contract and fails when that artifact has drifted without changing it.
- [HTTP API conventions](http-conventions.md)

The draft defines Phase 0 resources. ARC-032 defines cross-cutting HTTP behavior.
