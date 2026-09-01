# ENG-018 clean-checkout baseline

Validated 2026-09-01.

The Phase 1 baseline is exposed as `make baseline-verify`. It composes the
source gate with both hardened image smoke gates:

- repository hygiene and its isolated policy tests;
- exact supported-version pin validation;
- non-mutating Go format validation;
- Go vet and pinned Staticcheck;
- unit and race tests;
- pinned vulnerability scanning and Go/npm dependency-license policy;
- reproducible-path builds of `thinkpixelar`, `thinkpixel-agentd`, and
  `migrate`;
- OpenAPI lint, generation, and committed-artifact drift validation; and
- separate service and `thinkpixel-agentd` image builds and runtime smoke
  tests.

The OpenAPI check performs generation into a temporary file and compares it
byte-for-byte with the committed artifact. The format check obtains the Go
file set from Git and reports drift without changing the checkout. These
checks make the aggregate safe for a clean CI checkout and leave generated
source unchanged on success.

GitHub Actions supplies independent clean checkouts to the source, service
image, and agentd image jobs. The source job installs the exact npm lock file
before `make verify`; the image jobs run the two remaining prerequisites of
`make baseline-verify`. Splitting the aggregate across least-privilege jobs is
execution-equivalent and avoids giving the source-analysis job Docker access.

Local acceptance ran every `baseline-verify` prerequisite against the same
source snapshot. The `make verify` prerequisites passed in two invocations so
the hygiene fixture could use the small Linux `/tmp` filesystem while Go build
scratch used repository-local space. They covered format, generation,
vet/Staticcheck, unit, race, vulnerability/license, OpenAPI, and all three
binary builds. `make image-smoke agentd-image-smoke` built the immutable-base
images and passed their numeric non-root, read-only-filesystem,
capability-drop, and no-new-privileges probes. This is the Phase 1 build
baseline; it does not claim PostgreSQL integration, Kubernetes Agent Sandbox,
Kata, CSI, or release qualification.
