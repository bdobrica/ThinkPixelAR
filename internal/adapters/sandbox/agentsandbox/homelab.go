package agentsandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// HomelabPin is reviewed operator configuration, not learned from a receipt or
// workload. The initial reader supports only the qualified ARM64 resource lane.
type HomelabPin struct {
	Node, NodeUID, RuntimeClassUID string
	Artifacts                      map[string]string // qemu, kernel, image, runtime, configuration, cni
}

// HomelabEvidence is a short-lived trusted operator observation. It must be
// collected outside the workload, published atomically in the protected evidence
// directory, and never manufactured from PodSpec or agentd reports. See the
// runbook for the observation obligations of its publisher.
type HomelabEvidence struct {
	Version                                                 int
	ObservedAt, ExpiresAt                                   time.Time
	Scope                                                   sandbox.Scope
	RequestDigest, ProviderReference, PodUID, ContainerID   string
	NodeUID, RuntimeClassUID, Handler                       string
	Artifacts                                               map[string]string
	CRISandboxID                                            string
	QEMUPID                                                 int
	QEMUStartTicks                                          uint64
	KVMDescriptor                                           string
	ImageReference, ImageManifest, Architecture             string
	CPUQuota, CPUPeriod, MemoryMax, HardNProc, ScratchBytes int64
	WorkspaceReference                                      string
	// LiveObjects binds policy, storage and namespace observations to exact API
	// revisions. Node and RuntimeClass identity are separately checked below.
	LiveObjects []HomelabObject
	// Evidence digests identify retained trusted measurement records, not flags.
	NetworkMeasurement, MountMeasurement, ResourceMeasurement string
}

type HomelabObject struct {
	Resource, Namespace, Name, UID, ResourceVersion string
}

// HomelabVerifier rereads evidence and API identities on every invocation. It
// never caches a successful observation or grants authority. The evidence
// directory is a trusted AR-only mount, distinct from bootstrap and Workspace.
type HomelabVerifier struct {
	client dynamic.Interface
	root   *os.Root
	pin    HomelabPin
}

func NewHomelabVerifier(client dynamic.Interface, directory string, pin HomelabPin) (*HomelabVerifier, error) {
	if client == nil || !dnsName(pin.Node) || pin.NodeUID == "" || pin.RuntimeClassUID == "" || len(pin.Artifacts) != 6 {
		return nil, sandbox.ErrInvalid
	}
	for _, key := range []string{"qemu", "kernel", "image", "runtime", "configuration", "cni"} {
		if !shaDigest.MatchString(pin.Artifacts[key]) {
			return nil, sandbox.ErrInvalid
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, sandbox.ErrInvalid
	}
	info, err := root.Stat(".")
	if err != nil || info.Mode().Perm()&0077 != 0 || !trustedEvidenceOwner(info) {
		_ = root.Close()
		return nil, sandbox.ErrInvalid
	}
	pin.Artifacts = maps.Clone(pin.Artifacts)
	return &HomelabVerifier{client: client, root: root, pin: pin}, nil
}

func (v *HomelabVerifier) Close() error { return v.root.Close() }

func (v *HomelabVerifier) Verify(ctx context.Context, b sandbox.Binding, pod *v1.Pod) error {
	if pod == nil || pod.UID == "" || pod.Spec.NodeName != v.pin.Node || len(pod.Status.ContainerStatuses) != 1 || pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "kata-qemu-runtime-rs-ar331-bounded" {
		return sandbox.ErrIntegrity
	}
	// Kubernetes UIDs are opaque: disallow path components rather than assuming
	// their representation is a UUID.
	name := string(pod.UID)
	if !dnsName(name) {
		return sandbox.ErrIntegrity
	}
	f, err := v.root.OpenFile(name+".json", evidenceOpenFlags, 0)
	if err != nil {
		return sandbox.ErrIntegrity
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !trustedEvidenceOwner(info) || info.Size() > 32768 {
		return sandbox.ErrIntegrity
	}
	raw, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || len(raw) > 32768 {
		return sandbox.ErrIntegrity
	}
	var e HomelabEvidence
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF {
		return sandbox.ErrIntegrity
	}
	if v.check(b, pod, e, time.Now()) != nil {
		return sandbox.ErrIntegrity
	}
	obj, err := v.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).Get(ctx, v.pin.Node, metav1.GetOptions{})
	var node v1.Node
	if err != nil || runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &node) != nil || string(node.UID) != v.pin.NodeUID || node.DeletionTimestamp != nil || node.Status.NodeInfo.Architecture != "arm64" {
		return sandbox.ErrIntegrity
	}
	ready := false
	for _, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady && c.Status == v1.ConditionTrue {
			ready = true
		}
	}
	if !ready {
		return sandbox.ErrIntegrity
	}
	obj, err = v.client.Resource(schema.GroupVersionResource{Group: "node.k8s.io", Version: "v1", Resource: "runtimeclasses"}).Get(ctx, *pod.Spec.RuntimeClassName, metav1.GetOptions{})
	var class nodev1.RuntimeClass
	if err != nil || runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &class) != nil || string(class.UID) != v.pin.RuntimeClassUID || class.DeletionTimestamp != nil || class.Handler != e.Handler {
		return sandbox.ErrIntegrity
	}
	if class.Overhead == nil || class.Overhead.PodFixed.Cpu().MilliValue() != 250 || class.Overhead.PodFixed.Memory().Value() != 2415919104 {
		return sandbox.ErrIntegrity
	}
	claims := map[string]*v1.PersistentVolumeClaim{}
	volumes := map[string]*v1.PersistentVolume{}
	for _, ref := range e.LiveObjects {
		group := ""
		if ref.Resource == "networkpolicies" {
			group = "networking.k8s.io"
		}
		obj, err = v.client.Resource(schema.GroupVersionResource{Group: group, Version: "v1", Resource: ref.Resource}).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil || string(obj.GetUID()) != ref.UID || obj.GetResourceVersion() != ref.ResourceVersion || obj.GetDeletionTimestamp() != nil {
			return sandbox.ErrIntegrity
		}
		if ref.Resource == "persistentvolumeclaims" {
			var claim v1.PersistentVolumeClaim
			if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &claim) != nil || claim.Status.Phase != v1.ClaimBound || claim.Spec.VolumeName == "" {
				return sandbox.ErrIntegrity
			}
			claims[claim.Name] = &claim
		}
		if ref.Resource == "persistentvolumes" {
			var volume v1.PersistentVolume
			if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &volume) != nil || volume.Status.Phase != v1.VolumeBound {
				return sandbox.ErrIntegrity
			}
			volumes[volume.Name] = &volume
		}
	}
	for _, claim := range claims {
		volume := volumes[claim.Spec.VolumeName]
		if volume == nil || volume.Spec.ClaimRef == nil || volume.Spec.ClaimRef.UID != claim.UID || volume.Spec.ClaimRef.Name != claim.Name || volume.Spec.ClaimRef.Namespace != claim.Namespace {
			return sandbox.ErrIntegrity
		}
	}
	scratch, err := NewScratchVerifier(v.client, func(_ context.Context, _ *v1.Pod, claim *v1.PersistentVolumeClaim, volume *v1.PersistentVolume) error {
		c, p := claims[claim.Name], volumes[volume.Name]
		if c == nil || p == nil || c.UID != claim.UID || p.UID != volume.UID || c.ResourceVersion != claim.ResourceVersion || p.ResourceVersion != volume.ResourceVersion {
			return sandbox.ErrIntegrity
		}
		return nil // Physical backing was observed in this exact fresh receipt.
	})
	if err != nil || scratch(ctx, pod) != nil {
		return sandbox.ErrIntegrity
	}
	if ctx.Err() != nil || !e.ExpiresAt.After(time.Now()) {
		return sandbox.ErrIntegrity
	}
	return nil
}

func (v *HomelabVerifier) check(b sandbox.Binding, pod *v1.Pod, e HomelabEvidence, now time.Time) error {
	r := b.Request
	if e.Version != 1 || e.ObservedAt.After(now) || !e.ExpiresAt.After(now) || e.ExpiresAt.Sub(e.ObservedAt) <= 0 || e.ExpiresAt.Sub(e.ObservedAt) > 30*time.Second || e.Scope != r.Scope || e.RequestDigest != r.Operation.Digest || e.ProviderReference == "" || e.ProviderReference != b.ProviderReference || e.PodUID != string(pod.UID) || e.ContainerID == "" || e.ContainerID != pod.Status.ContainerStatuses[0].ContainerID {
		return sandbox.ErrIntegrity
	}
	if e.NodeUID != v.pin.NodeUID || e.RuntimeClassUID != v.pin.RuntimeClassUID || e.Handler != "kata-qemu-runtime-rs-ar331-bounded" || !maps.Equal(e.Artifacts, v.pin.Artifacts) || e.CRISandboxID == "" || e.QEMUPID <= 0 || e.QEMUStartTicks == 0 || e.KVMDescriptor != "anon_inode:kvm-vm" {
		return sandbox.ErrIntegrity
	}
	if r.Profile.Name != "coding-homelab-arm64" || r.Runtime.Architecture != "arm64" || e.Architecture != "arm64" || e.ImageReference != r.Runtime.Image || !shaDigest.MatchString(e.ImageManifest) || pod.Status.ContainerStatuses[0].ImageID != e.ImageManifest {
		return sandbox.ErrIntegrity
	}
	if e.CPUQuota != 100000 || e.CPUPeriod != 100000 || e.MemoryMax != 536870912 || e.HardNProc != 128 || e.ScratchBytes != 33554432 || r.Profile.Resources.CPU.Limit != 1000 || r.Profile.Resources.Memory.Limit != e.MemoryMax || r.Profile.Resources.MaxProcesses != e.HardNProc || e.WorkspaceReference == "" || e.WorkspaceReference != r.Workspace.Reference {
		return sandbox.ErrIntegrity
	}
	for _, digest := range []string{e.NetworkMeasurement, e.MountMeasurement, e.ResourceMeasurement} {
		if !shaDigest.MatchString(digest) {
			return sandbox.ErrIntegrity
		}
	}
	// Require exact namespace, one policy and all three volume claim/PV pairs.
	counts := map[string]int{}
	seen := map[string]bool{}
	claims := map[string]bool{}
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "workspace" || volume.Name == "state" {
			if volume.PersistentVolumeClaim == nil {
				return sandbox.ErrIntegrity
			}
			claims[volume.PersistentVolumeClaim.ClaimName] = true
		}
	}
	claims[pod.Name+"-tmp"] = true
	for _, ref := range e.LiveObjects {
		key := ref.Resource + "/" + ref.Namespace + "/" + ref.Name
		if ref.Name == "" || ref.UID == "" || ref.ResourceVersion == "" || seen[key] {
			return sandbox.ErrIntegrity
		}
		seen[key] = true
		switch ref.Resource {
		case "namespaces":
			if ref.Namespace != "" || ref.Name != pod.Namespace {
				return sandbox.ErrIntegrity
			}
		case "networkpolicies":
			if ref.Namespace != pod.Namespace {
				return sandbox.ErrIntegrity
			}
		case "persistentvolumeclaims":
			if ref.Namespace != pod.Namespace || !claims[ref.Name] {
				return sandbox.ErrIntegrity
			}
		case "persistentvolumes":
			if ref.Namespace != "" {
				return sandbox.ErrIntegrity
			}
		default:
			return sandbox.ErrIntegrity
		}
		counts[ref.Resource]++
	}
	if counts["namespaces"] != 1 || counts["networkpolicies"] != 1 || counts["persistentvolumeclaims"] != 3 || counts["persistentvolumes"] != 3 {
		return sandbox.ErrIntegrity
	}
	return nil
}
