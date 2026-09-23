# ADR-0049: Admit dependencies according to intended use and license obligations

Status: Accepted 2026-09-23

Supersedes only the local-demo license exception and expiry requirement in
[ADR-0048](0048-codex-demo-runtime-image.md). Its image design and qualification
requirements remain in effect.

## Decision

Allow any dependency license whose terms permit the intended use and whose
obligations can be met. Do not require blanket legal approval for GPL/AGPL,
MPL/LGPL or unfamiliar licenses. Use normal maintainer review to record the
selected terms, integration context and applicable obligations. Escalate only
unresolved rights or compatibility questions to the owner. Coding agents do not
independently research or qualify licensing unless explicitly asked; the owner
makes bundling and distribution decisions. Preserve notices, metadata and
automated inventory checks without representing them as legal clearance or
assuming research/testing establishes fair use. This does not relicense AR.

Separate permitted local use from redistribution and network-use obligations.
Independent GPL tools may accompany AR in an image with appropriate compliance;
linked or incorporated code needs a compatibility assessment. AGPL network terms
and commercial/restricted-use terms apply when their respective triggers occur.
A maintainer exception cannot waive an upstream license obligation.

Remove the CDX-002 calendar gate and retire its local-only exception. Preserve
that approval and original test evidence as history. Existing image evidence
still does not establish release source/notice compliance. Complete recorded
release requirements before distributing images to external recipients.

The automated Go/npm gates check pinned inventory and license metadata, without
license-family allowlists. They do not prove compatibility or corresponding-source
compliance. Retain security, provenance, architecture and supply-chain controls.

## Rationale

The blanket prohibition and expiring demo approval blocked ordinary permitted
uses without establishing redistribution compliance. Contextual review permits
the widest lawful dependency choice while keeping obligations attached to the
actual use and artifact. No new dependency, API or ThinkPixel boundary is added.

## Verification

See [policy change evidence](../evidence/dependency-license-policy.md).
