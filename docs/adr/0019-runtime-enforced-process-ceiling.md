# ADR-0019: Enforce the homelab process ceiling through a trusted OCI hard rlimit

- Status: Accepted
- Date: 2026-09-20
- Deciders: ThinkPixelAR maintainers
- Supersedes: None
- Superseded by: None

## Context

Kata 3.31.0 runtime-rs received OCI `linux.resources.pids.limit=128` from
containerd, but the live guest reported `pids.max=max`. A declared PID limit is
not effective enforcement. No production effective-state verifier is composed yet.

## Decision

Add a separate, opt-in canary handler whose containerd base OCI specification is
generated from the installed `ctr oci spec`. Add both `pids.limit=128` and hard/
soft `RLIMIT_NPROC=128`. The latter is the demonstrated bound for the tested
single-container, single-non-root-UID coding layout. All descendants inherit it;
no workload has privileges to raise the hard limit or switch identity. Qualify
this exact layout with a bounded fork test and an attempted hard-limit increase.
Do not treat guest cgroup PID enforcement as working on this tuple.

The original handler, default runc, published profiles and runtime mapping remain
unchanged. The canary handler is not a qualified secure profile: unresolved
scratch/storage bounds still prevent secure readiness. Installation and service
restart are explicit operator actions; generated containerd configuration is not
edited. Do not apply a different process ceiling underneath existing workloads.

## Alternatives considered

A host PID ceiling does not constrain guest processes. A harness-startup `ulimit`
is workload-controlled. Dropping the profile's process requirement would weaken
its contract. A trusted runtime hard rlimit works without buying infrastructure
or giving the workload additional authority.

## Consequences and security

The limit counts tasks for the guest UID, including runtime-created workload
threads, rather than promising 128 additional children. New UIDs, multiple
containers or privileged layouts require new qualification. A test observed 122
children before EAGAIN and EPERM when raising the hard limit to 256. This proves
a resource ceiling, not protection against every denial-of-service technique.

## Operations and compatibility

Use [the bounded-handler guide](../operations/kata-resource-checks.md). Only
worker02 has these diagnostic handlers. CPU/memory overhead in the diagnostic
RuntimeClass is a conservative experiment, not a capacity certification. Production
admission still requires independent complete infrastructure proof under ADR-0013.
No public contracts or profile digests change.

## References

- [Live resource findings](../evidence/kas-022-resource-findings.md)
- [containerd 2.3.4 runtime configuration](https://github.com/containerd/containerd/blob/v2.3.4/docs/cri/config.md)
