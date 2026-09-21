# AGD-001 — versioned agentd protocol and handshake

Implemented 2026-09-21; decision [ADR-0022](../adr/0022-agentd-protocol-handshake.md).

- Added `thinkpixel.agentd.v1` Protobuf source and generated Go types, with the
  bidirectional Connect descriptor and typed command/observation/acknowledgement,
  heartbeat, rotation and failure envelopes.
- Implemented compatibility negotiation against independently supplied binding,
  generation, nonce and immutable build/adapter expectations. Required capability
  mismatch, invalid limits, malformed identity, unknown/nested handshake fields,
  unsupported version and modified Welcome fail closed. Optional capabilities
  are explicitly intersected; input objects are not shared with the result.
- Generated-file comparison is part of `make verify`; stable service shape and
  published field-number tests protect wire compatibility. No new runtime module
  was added; existing Protobuf v1.36.12 is now a direct dependency.
- Verification: focused race tests, a bounded five-second/two-worker handshake
  fuzz run, generation drift check and full `make verify` passed.

The helper does not authenticate TLS, consume bootstrap material, allocate durable
connection epochs or authorize a process launch. Those responsibilities remain
AGD-003–005 and application composition. General envelope replay and adversarial
stream handling remain AGD-014/015. No live transport or cluster check is claimed.

Reproduction and wire semantics: [protocol reference](../contracts/agentd-protocol.md).
