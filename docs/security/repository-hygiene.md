# Repository hygiene

Status: Normative engineering policy.

ThinkPixelAR source history and release evidence MUST NOT contain local
Workspace or vendor state, kubeconfigs, generated sandbox credentials,
credential-bearing test fixtures, private keys, access-token formats, or
compiled binaries. These values are `Confidential` or `Restricted` under the
[data-classification contract](data-classification.md), and Git history is not
an approved store for them.

Run `make hygiene` to inspect the exact files staged in the Git index. The
aggregate `make verify` gate runs the same check in CI. The check rejects known
local-state and credential paths, binary content, private-key envelopes,
selected high-confidence access-token formats, and kubeconfig-shaped content.
It is a defense-in-depth admission control, not a general-purpose secret
manager or proof that arbitrary text contains no sensitive value.

Examples that need credential-shaped structure MUST use conspicuous inert
placeholders and safe filenames such as `.env.example`. Security tests MUST
construct canaries at runtime in temporary directories; they MUST NOT commit
credential values or credential-bearing fixtures. New runtime state paths,
vendor adapters, credential formats, or build outputs MUST update the ignore
and hygiene policies in the same change.
