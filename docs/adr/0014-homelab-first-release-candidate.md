# ADR-0014: Qualify the release candidate on existing homelab hardware

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: Initial amd64 production-size profile as a prerequisite for RC qualification in PLAN.md
- Superseded by: None

## Context

The maintainer explicitly selected the existing ARM64 homelab as the validation
lane and requested a low-friction RC without additional infrastructure costs.
The earlier coding profile targets larger amd64 nodes and encrypted snapshot-capable
storage. Requiring that infrastructure for RC would prevent practical evaluation.

## Decision

Use the existing homelab for Phase 3 / RC substrate evidence. Publish explicit
`coding-homelab-arm64` and `coding-homelab-amd64` profiles with the same core
microVM/credential/host/network security controls and smaller resource envelopes.
Each uses local single-writer storage, disables provider snapshots, requires no
at-rest encryption capability, and permits only mandatory platform-control egress.
Architecture, limits and storage choices are explicit immutable profile fields;
never substitute them into an existing Execution bound to another profile.

Keep `coding-medium-secure` unchanged as a larger deployment target. Full amd64,
encrypted snapshot/restore, distributed storage and production capacity testing
are future production qualification work, not blockers for the homelab RC. Record
concrete infrastructure needs and observed limitations with the release evidence.

## Alternatives considered

Buying/renting infrastructure now conflicts with the maintainer's RC objective.
Pretending the homelab proves production-scale storage/durability is misleading.
Silently changing the original profile would break immutable Session/Execution
bindings. Explicit profiles preserve both usability and honest capability claims.

## Consequences

A user with a compatible homelab or Linux PC can evaluate the bounded substrate
without new infrastructure. Unsupported snapshot/fork/node-loss recovery features
fail explicitly. Local data remains tied to its node; compute replacement on
that node is not evidence of node-loss durability. Profile publication alone is
not qualification: test the actual selected runtime, limits and network controls.

## Security

Retain Kata hardware virtualization, non-root execution, seccomp, dropped
capabilities, no API token/host access, and externally restricted networking.
Continue current ownership/authority and single-writer fencing. Local unencrypted
storage is an explicit operator trade-off, not suitable for a deployment requiring
encrypted storage. Core isolation/authority failures still block the tested lane.

## Operations

Start with one concurrent sandbox per worker and measure actual guest/host memory.
Do not use the installer overhead estimate as capacity proof. Record the exact
host/runtime/controller/CNI/storage tuple. CPU architecture and image manifests
must agree. Do not advertise untested PC configurations as qualified.

## Compatibility

No schema changes; profiles use existing abstract capabilities. ThinkPixel
ownership boundaries and optional integrations are unchanged. Existing profile
names, digests, bindings and accepted security contracts retain their meaning.

## References

- [Homelab RC and future infrastructure](../operations/rc-infrastructure.md)
- [ARM64 profile](../profiles/coding-homelab-arm64.json)
- [amd64 profile](../profiles/coding-homelab-amd64.json)
- [Original larger profile](../profiles/coding-medium-secure.json)
