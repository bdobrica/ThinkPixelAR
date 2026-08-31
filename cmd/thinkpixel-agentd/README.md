# thinkpixel-agentd

This command is the sandbox-local harness supervisor process. The ENG-014
baseline establishes a separately built, non-root executable and container
boundary; it waits for termination and exits cleanly.

It intentionally does not yet launch a harness or open a control-plane
transport. Those security-sensitive behaviors are implemented and tested in
Phase 4 against the normative [`agentd` contract](../../docs/contracts/agentd.md).
