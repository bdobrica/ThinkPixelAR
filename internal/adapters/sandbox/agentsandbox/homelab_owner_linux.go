package agentsandbox

import (
	"os"
	"syscall"
)

const evidenceOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

func trustedEvidenceOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}
