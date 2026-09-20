# KAS-013 — Desired and effective sandbox security

Added pre-reservation/pre-create enforcement for secure blueprints and a concrete
observed-Pod verification wrapper. Secure READY requires a separate trusted
infrastructure verifier, including actual image/runtime/network/mount/cgroup proof.

Race tests cover service-account automount and projected tokens; host network /
PID / IPC; hostPath/runtime socket; privilege and escalation; added capabilities;
root/writable root/unconfined seccomp; injected regular/init/ephemeral containers;
runtime/image/resource substitution; environment injection; runtime and alternate
network annotations; DNS overrides; missing image evidence; missing/failed
infrastructure qualification; defensive copies; and resource/mount bounds before
reservation. Unsafe desired compute never reaches binding reservation or creation.

Validation: focused provider race tests and `make verify`.
The qualification callback in positive unit tests is synthetic. Live hostile
probes and exact runtime/resource evidence are recorded in the later ARM64 lane;
this item does not imply full production-profile qualification.

Using real hardened blueprints also exposed structural comparison of Kubernetes
resource quantities across JSON serialization. Provider acquire/status/resume now
use Kubernetes semantic equality for blueprints: equivalent quantity encodings
compare correctly while resource/security drift still fails. The existing
HTTP-backed lifecycle replay tests exercise this round trip.
