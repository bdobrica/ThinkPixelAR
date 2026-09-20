package runtimeprofile

import "errors"

var ErrInvalidProfile = errors.New("invalid runtime profile")

// Profile is the abstract operator configuration; concrete substrate types are
// resolved only by infrastructure adapters. Treat values as immutable inputs.
type Profile struct {
	SchemaVersion  int            `json:"schema_version"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	IsolationClass string         `json:"isolation_class"`
	Resources      Resources      `json:"resources"`
	Storage        Storage        `json:"storage"`
	Network        Network        `json:"network"`
	Platform       Platform       `json:"platform"`
	Security       Security       `json:"security"`
	Lifecycle      Lifecycle      `json:"lifecycle"`
	Implementation Implementation `json:"implementation"`
}
type RequestLimit struct {
	Request int64 `json:"request"`
	Limit   int64 `json:"limit"`
}
type GPU struct {
	Count   int      `json:"count"`
	Classes []string `json:"classes"`
}
type Resources struct {
	CPU              RequestLimit `json:"cpu_millis"`
	Memory           RequestLimit `json:"memory_bytes"`
	EphemeralStorage RequestLimit `json:"ephemeral_storage_bytes"`
	MaxProcesses     int64        `json:"max_processes"`
	GPU              GPU          `json:"gpu,omitempty"`
}
type Storage struct {
	WorkspaceBytes     int64  `json:"workspace_bytes"`
	WorkspaceMount     string `json:"workspace_mount"`
	VendorStateRoot    string `json:"vendor_state_root"`
	AccessMode         string `json:"access_mode,omitempty"`
	SnapshotClass      string `json:"snapshot_class"`
	EncryptionRequired bool   `json:"encryption_required,omitempty"`
}
type Network struct {
	Profile            string `json:"profile"`
	DefaultDenyIngress bool   `json:"default_deny_ingress"`
	DefaultDenyEgress  bool   `json:"default_deny_egress"`
	DenyCloudMetadata  bool   `json:"deny_cloud_metadata"`
	DenyKubernetesAPI  bool   `json:"deny_kubernetes_api"`
	DNSPolicy          string `json:"dns_policy,omitempty"`
}
type Platform struct {
	OS            string   `json:"os"`
	Architectures []string `json:"architectures"`
	GPUAllowed    bool     `json:"gpu_allowed"`
	NodeClass     string   `json:"node_class,omitempty"`
}
type Security struct {
	Privileged               bool     `json:"privileged"`
	RunAsNonRoot             bool     `json:"run_as_non_root"`
	ReadOnlyRootFilesystem   bool     `json:"read_only_root_filesystem"`
	AllowPrivilegeEscalation bool     `json:"allow_privilege_escalation"`
	ServiceAccountToken      bool     `json:"service_account_token"`
	HostNetwork              bool     `json:"host_network"`
	HostPID                  bool     `json:"host_pid"`
	HostIPC                  bool     `json:"host_ipc"`
	HostPaths                bool     `json:"host_paths"`
	RuntimeSockets           bool     `json:"runtime_sockets"`
	LinuxCapabilitiesAdd     []string `json:"linux_capabilities_add"`
	SeccompClass             string   `json:"seccomp_class"`
}
type Lifecycle struct {
	IdleSuspendSeconds      int64  `json:"idle_suspend_seconds"`
	TerminationGraceSeconds int64  `json:"termination_grace_seconds"`
	WarmPoolEligible        bool   `json:"warm_pool_eligible"`
	SuspendMode             string `json:"suspend_mode,omitempty"`
}
type Implementation struct {
	ProviderKind        string `json:"provider_kind"`
	ProviderProfileRef  string `json:"provider_profile_ref"`
	IsolationRuntimeRef string `json:"isolation_runtime_ref"`
	StorageProfileRef   string `json:"storage_profile_ref"`
	NetworkPolicyRef    string `json:"network_policy_ref"`
}

// ValidateConstraints supplements schema validation; callers must also validate
// required fields and implementation capabilities before admitting a profile.
func (p Profile) ValidateConstraints() error {
	for _, r := range []RequestLimit{p.Resources.CPU, p.Resources.Memory, p.Resources.EphemeralStorage} {
		if r.Request > r.Limit {
			return ErrInvalidProfile
		}
	}
	g := p.Resources.GPU
	if g.Count > 0 && (!p.Platform.GPUAllowed || len(g.Classes) == 0) || g.Count == 0 && len(g.Classes) != 0 {
		return ErrInvalidProfile
	}
	if p.IsolationClass != "container-standard" {
		s, n := p.Security, p.Network
		if s.Privileged || !s.RunAsNonRoot || !s.ReadOnlyRootFilesystem || s.AllowPrivilegeEscalation || s.ServiceAccountToken || s.HostNetwork || s.HostPID || s.HostIPC || s.HostPaths || s.RuntimeSockets || len(s.LinuxCapabilitiesAdd) != 0 || !n.DefaultDenyIngress || !n.DefaultDenyEgress || !n.DenyCloudMetadata || !n.DenyKubernetesAPI || n.Profile == "unrestricted-standalone" {
			return ErrInvalidProfile
		}
	}
	if p.Network.Profile == "none" && p.Network.DNSPolicy != "disabled" {
		return ErrInvalidProfile
	}
	return nil
}
