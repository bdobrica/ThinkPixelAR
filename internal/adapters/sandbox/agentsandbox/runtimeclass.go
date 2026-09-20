package agentsandbox

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

// KataRuntimeMapping is operator-owned configuration. Its reference is the only
// value named by the neutral Runtime Profile. Qualification is independent of
// the existence or name of a RuntimeClass object.
type KataRuntimeMapping struct {
	Reference              string            `json:"reference"`
	KataVersion            string            `json:"kata_version"`
	RuntimeClass           string            `json:"runtime_class"`
	Handler                string            `json:"handler"`
	Architectures          []string          `json:"architectures"`
	NodeSelector           map[string]string `json:"node_selector"`
	OverheadCPUMillis      int64             `json:"overhead_cpu_millis"`
	OverheadMemoryBytes    int64             `json:"overhead_memory_bytes"`
	QualificationReference string            `json:"qualification_reference"`
}

// KataQualification verifies trusted host/guest evidence for this exact tuple,
// including artifacts, hardware virtualization, process/resource enforcement,
// node configuration and tested architecture. It returns the evidence digest.
// RuntimeClass fields alone may never implement this qualification function.
type KataQualification func(context.Context, runtimeprofile.Profile, KataRuntimeMapping, *nodev1.RuntimeClass) (string, error)

type ResolvedKataRuntime struct {
	Mapping         KataRuntimeMapping `json:"mapping"`
	RuntimeClassUID string             `json:"runtime_class_uid"`
	EvidenceDigest  string             `json:"evidence_digest"`
	Digest          string             `json:"-"`
}
type KataRuntimeResolver struct {
	client   dynamic.Interface
	mappings map[string]KataRuntimeMapping
	qualify  KataQualification
}

func NewKataRuntimeResolver(client dynamic.Interface, mappings []KataRuntimeMapping, qualify KataQualification) (*KataRuntimeResolver, error) {
	if client == nil || qualify == nil || len(mappings) == 0 || len(mappings) > 128 {
		return nil, sandbox.ErrInvalid
	}
	result := &KataRuntimeResolver{client: client, mappings: map[string]KataRuntimeMapping{}, qualify: qualify}
	for _, m := range mappings {
		if m.Reference == "" || len(m.Reference) > 255 || m.KataVersion != "3.31.0" || !dnsName(m.RuntimeClass) || !dnsName(m.Handler) || m.Handler == "runc" || m.QualificationReference == "" || len(m.QualificationReference) > 255 || m.OverheadCPUMillis <= 0 || m.OverheadMemoryBytes <= 0 || len(m.Architectures) == 0 || len(m.Architectures) > 2 || len(m.NodeSelector) == 0 {
			return nil, sandbox.ErrInvalid
		}
		if _, exists := result.mappings[m.Reference]; exists {
			return nil, sandbox.ErrConflict
		}
		seen := map[string]bool{}
		for _, arch := range m.Architectures {
			if arch != "amd64" && arch != "arm64" || seen[arch] {
				return nil, sandbox.ErrInvalid
			}
			seen[arch] = true
			if selected := m.NodeSelector["kubernetes.io/arch"]; selected != "" && selected != arch {
				return nil, sandbox.ErrInvalid
			}
		}
		for key, value := range m.NodeSelector {
			if len(validation.IsQualifiedName(key)) != 0 || len(validation.IsValidLabelValue(value)) != 0 || value == "" {
				return nil, sandbox.ErrInvalid
			}
		}
		if os := m.NodeSelector["kubernetes.io/os"]; os != "" && os != "linux" {
			return nil, sandbox.ErrInvalid
		}
		m.Architectures = slices.Clone(m.Architectures)
		m.NodeSelector = maps.Clone(m.NodeSelector)
		result.mappings[m.Reference] = m
	}
	return result, nil
}
func (r *KataRuntimeResolver) Resolve(ctx context.Context, p runtimeprofile.Profile) (ResolvedKataRuntime, error) {
	fail := ResolvedKataRuntime{}
	m, ok := r.mappings[p.Implementation.IsolationRuntimeRef]
	if !ok || p.ValidateConstraints() != nil || p.IsolationClass != "microvm-strong" || p.Platform.OS != "linux" || len(p.Platform.Architectures) == 0 {
		return fail, sandbox.ErrUnsupported
	}
	for _, arch := range p.Platform.Architectures {
		if !slices.Contains(m.Architectures, arch) {
			return fail, sandbox.ErrUnsupported
		}
	}
	obj, err := r.client.Resource(schema.GroupVersionResource{Group: "node.k8s.io", Version: "v1", Resource: "runtimeclasses"}).Get(ctx, m.RuntimeClass, metav1.GetOptions{})
	if err != nil {
		return fail, providerError(err)
	}
	var observed nodev1.RuntimeClass
	if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &observed) != nil || observed.UID == "" || observed.Name != m.RuntimeClass || observed.DeletionTimestamp != nil || observed.Handler != m.Handler || observed.Scheduling == nil || !maps.Equal(observed.Scheduling.NodeSelector, m.NodeSelector) || len(observed.Scheduling.Tolerations) != 0 || observed.Overhead == nil || len(observed.Overhead.PodFixed) != 2 || observed.Overhead.PodFixed.Cpu().MilliValue() != m.OverheadCPUMillis || observed.Overhead.PodFixed.Memory().Value() != m.OverheadMemoryBytes {
		return fail, sandbox.ErrIntegrity
	}
	m.Architectures = slices.Clone(m.Architectures)
	m.NodeSelector = maps.Clone(m.NodeSelector)
	profileJSON, err := json.Marshal(p)
	if err != nil {
		return fail, sandbox.ErrInvalid
	}
	var profileCopy runtimeprofile.Profile
	if json.Unmarshal(profileJSON, &profileCopy) != nil {
		return fail, sandbox.ErrInvalid
	}
	evidence, err := r.qualify(ctx, profileCopy, m, observed.DeepCopy())
	if err != nil || !shaDigest.MatchString(evidence) {
		return fail, sandbox.ErrUnsupported
	}
	// Recopy after callback: never expose the resolver's internal maps or slices.
	m = r.mappings[p.Implementation.IsolationRuntimeRef]
	m.Architectures = slices.Clone(m.Architectures)
	m.NodeSelector = maps.Clone(m.NodeSelector)
	result := ResolvedKataRuntime{Mapping: m, RuntimeClassUID: string(observed.UID), EvidenceDigest: evidence}
	raw, err := json.Marshal(result)
	if err != nil {
		return fail, sandbox.ErrInvalid
	}
	raw, err = canonical.Transform(raw)
	if err != nil {
		return fail, sandbox.ErrInvalid
	}
	result.Digest = sandbox.Digest(raw)
	return result, nil
}
