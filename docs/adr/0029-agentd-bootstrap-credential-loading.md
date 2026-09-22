# ADR-0029: Agentd bootstrap credential loading

Status: Accepted 2026-09-22

## Context

ADR-0023 implements read-only config loading; ADR-0028 defines the protected
seven-file Secret layout. Reading projected-volume symlinks separately could mix
material from different generations. Loading must remain sandbox-local and must
not grant authority or install a permissive frame handler.

## Decision

`agentd.LoadTransport` reads only the fixed `/run/thinkpixel/bootstrap` root.
For a Kubernetes projection, it opens `..data` once with `os.Root.OpenRoot` and
reads all seven files relative to that pinned directory. Otherwise it reads the
direct mount. Root-relative opens reject symlink escape. Each file must be regular,
have no write permission bits and reside on a read-only filesystem as checked
through its opened descriptor. Nonblocking opens avoid FIFO hangs. Reads have the
per-file ceilings in ADR-0028. Non-Linux credential loading fails closed.

The complete bundle is decoded before returning usable material. Existing config
validation applies; proof/challenge are exactly 32 bytes. The key must match the
certificate, and the transport's identity validator checks clientAuth, exact
tenant/Sandbox/Attempt URI, strong leaf key, current validity, at most ten-minute
bootstrap lifetime, and separation from server trust. The delivered client chain
must verify its signature/EKU/time consistency; its supplied root does not become
AR authority. AR independently verifies its configured dedicated client roots.

Server trust contains at most four current CA certificates with strict PEM
framing and no trailing data. There is no system-root, environment, Kubernetes
client or metadata-discovery fallback. Loading makes no network call.

The parsed bootstrap object keeps credentials private, redacts standard Go
formatting and returns copies of config/handshake byte buffers. Constructing a
transport client config requires a non-nil frame Check and revalidates bootstrap
identity/expiry. No default Check is provided. Explicit Destroy clears owned byte
buffers and drops parsed key references; Go/TLS copies and big integers cannot
be guaranteed erased. Callers must close clients before destruction.

Both existing config loading and the new loader share the descriptor-based
bounded file reader. Existing startup remains config-only and dormant until
admission/rotation composition is complete; this adds no CLI/environment switch,
listener or harness launch. Earlier config-only behavior remains compatible.

## Consequences

Credential decoding and protected filesystem loading are implemented. AGD-005
still requires durable bootstrap plan/UID/cleanup orchestration, concrete policy
adapters and binary/rotation composition. Successful local loading authenticates
neither the sandbox's claimed build nor its authority to execute.

Tests cover decoder bounds, mismatched identity/key/trust, private copies and
destruction, writable mounts, FIFO and projection escape. A fresh static test
binary runs successfully in an unprivileged local container with read-only
mounts, no network and no capabilities, for direct and pinned projected layouts.
This is filesystem qualification, not live Kubernetes or full protocol deployment.

See [evidence](../evidence/agd-005-credential-loading.md).
