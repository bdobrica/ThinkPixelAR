# ADR-0035: Agentd credential persistence exclusion

Status: Accepted 2026-09-22

## Decision

AGD-011 retains the structural separation established by ADRs 0023, 0029, 0030,
0031 and 0034. Agentd has no generic environment, home-directory, process-image
or credential-file export into vendor state. The checkpoint component passes
only declared roots and candidate metadata, never a transport bootstrap object,
process environment or credential descriptor. The existing implementation needs
no new persistence API or secret-scanning dependency to meet this local no-copy
requirement.

Bootstrap configuration excludes execution environment and credential fields.
Transport credentials remain private in-memory material loaded from the fixed
read-only runtime mount. Harness start and restart use an explicit empty
environment and no extra descriptors. Output capture stays in bounded memory,
with no disk spill. Checkpoint preparation performs no filesystem copying.
Errors remain fixed codes rather than adapter-provided strings.

Regression tests generate transient canaries and keep a supervisor-owned
credential descriptor open while a real child tries to persist inherited
material. They verify empty persisted environment/descriptor observations,
unchanged state through successful and failed preparation, clean restart/stop,
non-secret metadata, rejection of transient credential locations, and closed
bootstrap configuration.

## Limits

This closes the requirement that agentd itself does not copy execution-scoped
material into persistent state. It does not establish resistance to a hostile
harness reading its own authority or persisting credentials it receives. No
Execution credential injection exists yet; its future adapter must use the
[ephemeral injection contract](../security/execution-credentials.md), extend
these canary tests, and avoid persistent home/cache paths.

The trusted Phase 6 checkpoint producer still independently validates files,
mounts, credential exclusions and canary scans before publication. Local lexical
path checks cannot detect a symlink, a bind mount or secret file content. No live
checkpoint or gateway qualification is claimed. Authenticated binary composition
remains AGD-020. There are no wire, schema, dependency or ownership changes.

See [AGD-011 evidence](../evidence/agd-011-credential-exclusion.md).
