package sandbox

import (
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
)

// Capabilities describes implemented provider vocabulary, not permission or
// physical qualification. Digest binds the trusted discovery evidence used at
// admission; the immutable implementation snapshot must include it.
type Capabilities struct {
	ProviderKind     string   `json:"provider_kind"`
	ContractVersion  string   `json:"contract_version"`
	ProviderVersion  string   `json:"provider_version"`
	SupportsSuspend  bool     `json:"supports_suspend"`
	SupportsResume   bool     `json:"supports_resume"`
	SupportsWarmPool bool     `json:"supports_warm_pool"`
	IsolationClasses []string `json:"isolation_classes"`
	Architectures    []string `json:"architectures"`
	VolumeAttachment []string `json:"volume_attachment"`
	NetworkClasses   []string `json:"network_classes"`
	Digest           string   `json:"digest"`
}

func (c Capabilities) Clone() Capabilities {
	c.IsolationClasses = slices.Clone(c.IsolationClasses)
	c.Architectures = slices.Clone(c.Architectures)
	c.VolumeAttachment = slices.Clone(c.VolumeAttachment)
	c.NetworkClasses = slices.Clone(c.NetworkClasses)
	return c
}

// ValidateProfile checks the provider-owned requirements only. Storage backend,
// authority, runtime artifacts and effective security require their own checks.
func (c Capabilities) ValidateProfile(p runtimeprofile.Profile) error {
	if c.ProviderKind != p.Implementation.ProviderKind || c.ContractVersion != "v1" || c.Digest == "" || p.ValidateConstraints() != nil || p.Platform.OS != "linux" || len(p.Platform.Architectures) == 0 || !slices.Contains(c.IsolationClasses, p.IsolationClass) || !slices.Contains(c.NetworkClasses, p.Network.Profile) || !slices.Contains(c.VolumeAttachment, p.Storage.AccessMode) || p.Platform.GPUAllowed || p.Resources.GPU.Count != 0 || p.Lifecycle.WarmPoolEligible && !c.SupportsWarmPool {
		return ErrUnsupported
	}
	for _, arch := range p.Platform.Architectures {
		if !slices.Contains(c.Architectures, arch) {
			return ErrUnsupported
		}
	}
	if p.Lifecycle.SuspendMode != "release-and-restore" && p.Lifecycle.SuspendMode != "provider-suspend-preferred" {
		return ErrUnsupported
	}
	if p.Lifecycle.SuspendMode == "provider-suspend-preferred" && (!c.SupportsSuspend || !c.SupportsResume) {
		return ErrUnsupported
	}
	return nil
}
