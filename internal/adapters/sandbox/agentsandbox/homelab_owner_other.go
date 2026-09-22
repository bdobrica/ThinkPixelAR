//go:build !linux

package agentsandbox

import "os"

const evidenceOpenFlags = os.O_RDONLY

func trustedEvidenceOwner(os.FileInfo) bool { return false }
