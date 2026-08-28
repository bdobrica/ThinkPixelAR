# ThinkPixelAR Documentation

This directory contains the durable architecture and security contracts for ThinkPixelAR. Planning intent begins in `PLAN.md`; once a decision is accepted, its normative form belongs here.

- [Supported component versions](supported-versions.md)

## Structure

- `adr/` — numbered architecture decision records created from `adr/template.md`;
- `architecture/` — system context, trust boundaries, and component-level design;
- `security/` — threat models, data handling, and security invariants;
- `contracts/` — vendor-neutral domain and runtime contracts;
- `api/` — public OpenAPI and HTTP protocol contracts;
- `profiles/` — reviewed example Runtime Profile instances;
- `operations/` — deployment, recovery, capacity, and operational guidance;
- `evidence/` — phase and release verification evidence.

## Document conventions

- Use normative terms (`MUST`, `MUST NOT`, `SHOULD`, `SHOULD NOT`, and `MAY`) as defined by RFC 2119 and RFC 8174 only where a requirement is intended.
- Prefer Mermaid for diagrams so they remain reviewable as text.
- Use relative links for repository-local references.
- Record unresolved decisions explicitly; do not present assumptions as accepted contracts.
- Never include credentials, tokens, private repository content, prompts, model output, or other sensitive runtime data in documentation or evidence.

## Architecture decision records

Copy `adr/template.md` to the next zero-padded number, for example `adr/0001-example.md`. ADRs are immutable after acceptance except for typo or link corrections. A later decision supersedes an earlier one and links both records.
