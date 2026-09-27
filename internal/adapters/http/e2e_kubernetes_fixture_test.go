package http

// Test-only infrastructure composition. It uses the operator's SSH/Kubernetes
// identity outside the guest; no Kubernetes or database credential enters a Pod.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
)

type e2eKube struct {
	t                                     *testing.T
	ctx                                   context.Context
	host, namespace, image, node, runtime string
}
type e2eCompute struct{ name, sandboxUID, podUID, workspaceUID, stateUID string }
type e2eGuestResult struct {
	Phase, Thread, SHA256, WorkspaceSHA256, HistoryMarker string
	Calls                                                 int
	History                                               bool
	Binding                                               *agentdv1.Binding
}

func e2eQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (k *e2eKube) command(input []byte, args ...string) ([]byte, error) {
	words := []string{"sudo", "kubectl"}
	words = append(words, args...)
	for n := range words {
		words[n] = e2eQuote(words[n])
	}
	ctx, cancel := context.WithTimeout(k.ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-F", filepath.Join(os.Getenv("HOME"), ".ssh/config"), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", k.host, strings.Join(words, " "))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("kubectl %s failed: %w: %.1024s", args[0], err, stderr.String())
	}
	if len(out) > 2<<20 {
		return nil, fmt.Errorf("oversized Kubernetes response")
	}
	return out, nil
}
func (k *e2eKube) must(input []byte, args ...string) []byte {
	k.t.Helper()
	out, e := k.command(input, args...)
	if e != nil {
		k.t.Fatal(e)
	}
	return out
}
func (k *e2eKube) create(v any) {
	k.t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		k.t.Fatal(e)
	}
	k.must(raw, "create", "-f", "-")
}
func (k *e2eKube) object(kind, name string) map[string]any {
	k.t.Helper()
	var v map[string]any
	if e := json.Unmarshal(k.must(nil, "-n", k.namespace, "get", kind, name, "-o", "json"), &v); e != nil {
		k.t.Fatal(e)
	}
	return v
}
func e2eUID(v map[string]any) string { return v["metadata"].(map[string]any)["uid"].(string) }

func newE2EKube(t *testing.T, ctx context.Context) *e2eKube {
	t.Helper()
	k := &e2eKube{t: t, ctx: ctx, host: os.Getenv("THINKPIXELAR_E2E_SSH"), namespace: "ar-e2e001-" + time.Now().UTC().Format("20060102150405") + fmt.Sprintf("-%d", os.Getpid()), image: os.Getenv("THINKPIXELAR_E2E_IMAGE"), node: "k3spi-02", runtime: "kata-qemu-runtime-rs-ar331"}
	if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(k.host) || !regexp.MustCompile(`^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$`).MatchString(k.image) {
		t.Fatal("explicit SSH alias and immutable probe image required")
	}
	k.create(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": k.namespace, "labels": map[string]string{"pod-security.kubernetes.io/enforce": "restricted", "thinkpixelar-e2e": "e2e001"}}})
	t.Logf("test namespace=%s (retained for evidence; delete only this namespace after review)", k.namespace)
	k.create(map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]string{"name": "deny-all", "namespace": k.namespace}, "spec": map[string]any{"podSelector": map[string]any{}, "policyTypes": []string{"Ingress", "Egress"}}})
	return k
}

func (k *e2eKube) acquire(name string) e2eCompute {
	k.t.Helper()
	for _, suffix := range []string{"workspace", "state"} {
		k.create(map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]string{"name": name + "-" + suffix, "namespace": k.namespace}, "spec": map[string]any{"storageClassName": "local-path", "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": "256Mi"}}}})
	}
	pod := map[string]any{"runtimeClassName": k.runtime, "automountServiceAccountToken": false, "enableServiceLinks": false, "restartPolicy": "Never", "activeDeadlineSeconds": 1800, "terminationGracePeriodSeconds": 5, "nodeSelector": map[string]string{"kubernetes.io/hostname": k.node, "kubernetes.io/arch": "arm64"}, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "probe", "image": k.image, "imagePullPolicy": "Never", "command": []string{"/bin/sleep", "1800"}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "250m", "memory": "512Mi"}, "limits": map[string]string{"cpu": "2", "memory": "1Gi"}}, "volumeMounts": []any{map[string]string{"name": "workspace", "mountPath": "/workspace"}, map[string]string{"name": "state", "mountPath": "/state"}, map[string]string{"name": "scratch", "mountPath": "/tmp"}}}}, "volumes": []any{map[string]any{"name": "workspace", "persistentVolumeClaim": map[string]string{"claimName": name + "-workspace"}}, map[string]any{"name": "state", "persistentVolumeClaim": map[string]string{"claimName": name + "-state"}}, map[string]any{"name": "scratch", "emptyDir": map[string]string{"medium": "Memory", "sizeLimit": "128Mi"}}}}
	k.create(map[string]any{"apiVersion": "agents.x-k8s.io/v1beta1", "kind": "Sandbox", "metadata": map[string]string{"name": name, "namespace": k.namespace}, "spec": map[string]any{"operatingMode": "Running", "service": false, "shutdownTime": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339), "shutdownPolicy": "Retain", "podTemplate": map[string]any{"spec": pod}}})
	// The pinned controller names the owned Pod after the Sandbox.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if _, e := k.command(nil, "-n", k.namespace, "get", "pod", name); e == nil {
			break
		}
		if time.Now().After(deadline) {
			k.t.Fatal("Sandbox Pod not created")
		}
		select {
		case <-k.ctx.Done():
			k.t.Fatal(k.ctx.Err())
		case <-time.After(time.Second):
		}
	}
	k.must(nil, "-n", k.namespace, "wait", "pod/"+name, "--for=condition=Ready", "--timeout=120s")
	sb, p := k.object("sandbox", name), k.object("pod", name)
	uid := e2eUID(sb)
	owned := false
	for _, o := range p["metadata"].(map[string]any)["ownerReferences"].([]any) {
		if o.(map[string]any)["uid"] == uid {
			owned = true
		}
	}
	if !owned || p["spec"].(map[string]any)["runtimeClassName"] != k.runtime {
		k.t.Fatal("unexpected Pod ownership/runtime")
	}
	c := e2eCompute{name, uid, e2eUID(p), e2eUID(k.object("pvc", name+"-workspace")), e2eUID(k.object("pvc", name+"-state"))}
	k.t.Logf("Sandbox=%s/%s uid=%s Pod uid=%s runtime=%s", k.namespace, name, c.sandboxUID, c.podUID, k.runtime)
	k.hostProof(c)
	return c
}

func (k *e2eKube) hostProof(c e2eCompute) {
	k.t.Helper()
	script, err := os.ReadFile("../../../test/security/kata-host-proof.py")
	e2eCheck(k.t, err)
	args := []string{"sudo", "python3", "-", "--name", c.name, "--namespace", k.namespace, "--uid", c.podUID, "--handler", k.runtime}
	for n := range args {
		args[n] = e2eQuote(args[n])
	}
	ctx, cancel := context.WithTimeout(k.ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-F", filepath.Join(os.Getenv("HOME"), ".ssh/config"), "-i", filepath.Join(os.Getenv("HOME"), ".ssh/id_k3spi"), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "pi@10.10.10.12", strings.Join(args, " "))
	cmd.Stdin = bytes.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		k.t.Fatalf("Kata host proof failed: %v %.2048s", err, out)
	}
	var proof struct {
		KVM    bool   `json:"kvm_vm_descriptor"`
		PodUID string `json:"pod_uid"`
	}
	e2eCheck(k.t, json.Unmarshal(out, &proof))
	if !proof.KVM || proof.PodUID != c.podUID {
		k.t.Fatal("KVM proof did not match live Pod")
	}
	k.t.Logf("trusted worker QEMU/KVM and pinned artifacts verified for Pod uid=%s", c.podUID)
}
func (k *e2eKube) read(c e2eCompute, path string) []byte {
	return k.must(nil, "-n", k.namespace, "exec", c.name, "--", "cat", path)
}
func (k *e2eKube) write(c e2eCompute, path string, raw []byte) {
	k.t.Helper()
	// Paths are selected solely by the test controller, never vendor content.
	if path != "/workspace/context.txt" && path != "/state/rollout.jsonl" && path != "/state/restore.json" {
		k.t.Fatal("unexpected restore destination")
	}
	k.must(raw, "-n", k.namespace, "exec", "-i", c.name, "--", "/bin/sh", "-c", "umask 077; set -C; cat > "+e2eQuote(path)+" && sync")
}
func (k *e2eKube) guest(c e2eCompute, phase string, b *agentdv1.Binding) e2eGuestResult {
	k.t.Helper()
	raw, _ := json.Marshal(b)
	out, err := k.command(nil, "-n", k.namespace, "exec", c.name, "--", "env", "THINKPIXELAR_E2E_GUEST="+phase, "THINKPIXELAR_E2E_BINDING="+string(raw), "/probe/agentd.test", "-test.run=^TestStandaloneKubernetesGuest$", "-test.v", "-test.timeout=90s")
	if err != nil {
		k.t.Fatalf("guest %s failed: %v %.2048s", phase, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "E2E_GUEST_RESULT ") {
			var r e2eGuestResult
			if e := json.Unmarshal([]byte(strings.TrimPrefix(line, "E2E_GUEST_RESULT ")), &r); e != nil {
				k.t.Fatal(e)
			}
			if r.Phase != phase || r.Binding == nil || r.Binding.TenantId != b.TenantId || r.Binding.SessionId != b.SessionId || r.Binding.ExecutionId != b.ExecutionId || r.Binding.AttemptId != b.AttemptId || r.Binding.SandboxBindingId != b.SandboxBindingId || r.Binding.SessionGeneration != b.SessionGeneration {
				k.t.Fatal("guest identity mismatch")
			}
			return r
		}
	}
	k.t.Fatalf("guest did not report success: %.2048s", out)
	return e2eGuestResult{}
}
func (k *e2eKube) release(c e2eCompute) {
	k.t.Helper()
	if e2eUID(k.object("sandbox", c.name)) != c.sandboxUID || e2eUID(k.object("pod", c.name)) != c.podUID {
		k.t.Fatal("refusing to delete different compute")
	}
	k.must(nil, "-n", k.namespace, "delete", "sandbox", c.name, "--wait=true", "--timeout=120s")
	k.must(nil, "-n", k.namespace, "wait", "--for=delete", "pod/"+c.name, "--timeout=120s")
	for _, kind := range []string{"sandbox", "pod"} {
		if out := k.must(nil, "-n", k.namespace, "get", kind, c.name, "--ignore-not-found", "-o", "name"); len(bytes.TrimSpace(out)) != 0 {
			k.t.Fatal("old compute still exists")
		}
	}
	k.t.Logf("confirmed absent Sandbox uid=%s and Pod uid=%s", c.sandboxUID, c.podUID)
}

// Only the disposable fixture PVCs are removed, after checkpoint publication
// and compute absence. This is not a production Workspace deletion operation.
func (k *e2eKube) discardOldVolumes(c e2eCompute) {
	k.t.Helper()
	for suffix, uid := range map[string]string{"workspace": c.workspaceUID, "state": c.stateUID} {
		name := c.name + "-" + suffix
		if e2eUID(k.object("pvc", name)) != uid {
			k.t.Fatal("refusing different fixture PVC")
		}
		k.must(nil, "-n", k.namespace, "delete", "pvc", name, "--wait=true", "--timeout=120s")
		if out := k.must(nil, "-n", k.namespace, "get", "pvc", name, "--ignore-not-found", "-o", "name"); len(bytes.TrimSpace(out)) != 0 {
			k.t.Fatal("old fixture PVC still exists")
		}
	}
	k.t.Logf("old fixture PVCs absent workspace=%s state=%s; restore requires external export", c.workspaceUID, c.stateUID)
}
