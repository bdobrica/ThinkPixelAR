# KAS-012 — Operator-controlled Kata RuntimeClass mapping

Implemented an immutable runtime-reference resolver against the exact installed
RuntimeClass, with mandatory independent qualification and canonical mapping /
evidence / UID digest. Tests exercise expected handler selection, stable copied
resolution, missing mapping/qualification, architecture mismatch, substituted
runc handler, changed selectors/tolerations and missing overhead.

The [homelab mapping](../../deploy/kata/runtime-mapping.json) records its existing
Kata 3.31.0 handler, ARM64 architecture, configured-node selectors and installer
250m/160Mi overhead. It is an operator candidate, not an admission grant. The
initial coding profile remains amd64 and is rejected by this mapping. Qualification
must also reject the unmeasured overhead until KAS-021/022 evidence supports it.

Validation: focused provider race tests and `make verify`. RuntimeClass tests use
HTTP-backed API fixtures; no new physical qualification claim is made. Existing
live installation evidence is retained separately. Storage/network qualification
and service composition remain required before production admission.
