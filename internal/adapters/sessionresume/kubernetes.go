//go:build linux

package sessionresume

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

// The SSH alias resolves through the operator's existing configuration. Every
// remote word is quoted, all output bounded, and command errors contain no
// provider diagnostics or restored bytes. Credentials never enter the sandbox.
type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, workspace.ErrUnavailable
	}
	return b.Buffer.Write(p)
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func remote(ctx context.Context, host string, input []byte, words ...string) ([]byte, error) {
	for n := range words {
		words[n] = quote(words[n])
	}
	args := []string{"-F", filepath.Join(os.Getenv("HOME"), ".ssh/config"), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	if host == "pi@10.10.10.12" {
		args = append(args, "-i", filepath.Join(os.Getenv("HOME"), ".ssh/id_k3spi"))
	}
	args = append(args, host, strings.Join(words, " "))
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = bytes.NewReader(input)
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if cmd.Run() != nil {
		return nil, workspace.ErrUnavailable
	}
	return out.Bytes(), nil
}
func (b *Bundle) command(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	return remote(ctx, b.config.SSH, input, append([]string{"sudo", "kubectl", "-n", b.config.Namespace}, args...)...)
}
func (b *Bundle) get(ctx context.Context, kind, name string) (map[string]any, error) {
	raw, err := b.command(ctx, nil, "get", kind, name, "--ignore-not-found", "-o", "json")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return nil, workspace.ErrIntegrity
	}
	return v, nil
}
func metadata(v map[string]any) map[string]any     { m, _ := v["metadata"].(map[string]any); return m }
func uid(v map[string]any) string                  { s, _ := metadata(v)["uid"].(string); return s }
func candidateName(i sessions.ResumeIntent) string { return "resume-" + string(i.SandboxID) }
func owned(v map[string]any, i sessions.ResumeIntent) bool {
	a, _ := metadata(v)["annotations"].(map[string]any)
	return uid(v) != "" && a["thinkpixel.io/resume-intent"] == i.Digest()
}
func subset(expected, actual any) bool {
	if m, ok := expected.(map[string]any); ok {
		v, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, e := range m {
			if !subset(e, v[k]) {
				return false
			}
		}
		return true
	}
	if a, ok := expected.([]any); ok {
		v, ok := actual.([]any)
		if !ok || len(a) != len(v) {
			return false
		}
		for n, e := range a {
			if !subset(e, v[n]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(expected, actual)
}
func normalized(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
func (b *Bundle) ensure(ctx context.Context, kind string, v map[string]any, i sessions.ResumeIntent) error {
	name := metadata(v)["name"].(string)
	found, err := b.get(ctx, kind, name)
	if err != nil {
		return err
	}
	if found == nil {
		raw, _ := json.Marshal(v)
		if _, err = b.command(ctx, raw, "create", "-f", "-", "-o", "json"); err != nil {
			return err
		}
		found, err = b.get(ctx, kind, name)
		if err != nil {
			return err
		}
	}
	if !owned(found, i) || metadata(found)["deletionTimestamp"] != nil || !subset(normalized(v["spec"]), found["spec"]) {
		return workspace.ErrIntegrity
	}
	return nil
}
func (b *Bundle) namespace(ctx context.Context) error {
	ns, err := b.get(ctx, "namespace", b.config.Namespace)
	if err != nil {
		return err
	}
	labels, _ := metadata(ns)["labels"].(map[string]any)
	if uid(ns) != b.config.NamespaceUID || labels["pod-security.kubernetes.io/enforce"] != "restricted" || metadata(ns)["deletionTimestamp"] != nil {
		return workspace.ErrIntegrity
	}
	raw, err := b.command(ctx, nil, "get", "networkpolicy", "-o", "json")
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			Spec struct {
				PodSelector     map[string]any `json:"podSelector"`
				Ingress, Egress []any
				PolicyTypes     []string `json:"policyTypes"`
			}
		}
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Items) != 1 {
		return workspace.ErrIntegrity
	}
	p := list.Items[0].Spec
	if len(p.PodSelector) != 0 || len(p.Ingress) != 0 || len(p.Egress) != 0 || len(p.PolicyTypes) != 2 || !(p.PolicyTypes[0] == "Ingress" && p.PolicyTypes[1] == "Egress" || p.PolicyTypes[1] == "Ingress" && p.PolicyTypes[0] == "Egress") {
		return workspace.ErrIntegrity
	}
	return nil
}
func (b *Bundle) spec(name string) map[string]any {
	pod := map[string]any{"runtimeClassName": "kata-qemu-runtime-rs-ar331-bounded", "automountServiceAccountToken": false, "enableServiceLinks": false, "restartPolicy": "Never", "activeDeadlineSeconds": 1800, "terminationGracePeriodSeconds": 5, "nodeSelector": map[string]string{"kubernetes.io/hostname": "k3spi-02", "kubernetes.io/arch": "arm64"}, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "probe", "image": b.config.Image, "imagePullPolicy": "Never", "command": []string{"/usr/local/bin/thinkpixel-resume-probe", "-idle"}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "250m", "memory": "256Mi", "ephemeral-storage": "64Mi"}, "limits": map[string]string{"cpu": "1", "memory": "512Mi", "ephemeral-storage": "128Mi"}}, "volumeMounts": []any{map[string]string{"name": "workspace", "mountPath": "/workspace"}, map[string]string{"name": "state", "mountPath": "/state"}, map[string]string{"name": "scratch", "mountPath": "/tmp"}}}}, "volumes": []any{map[string]any{"name": "workspace", "persistentVolumeClaim": map[string]string{"claimName": name + "-workspace"}}, map[string]any{"name": "state", "persistentVolumeClaim": map[string]string{"claimName": name + "-state"}}, map[string]any{"name": "scratch", "ephemeral": map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{"storageClassName": "ar-bounded-scratch-v1", "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": "32Mi"}}}}}}}}
	return pod
}
func (b *Bundle) allocate(ctx context.Context, i sessions.ResumeIntent) error {
	if err := b.namespace(ctx); err != nil {
		return err
	}
	name := candidateName(i)
	meta := func(n string) map[string]any {
		return map[string]any{"name": n, "namespace": b.config.Namespace, "annotations": map[string]string{"thinkpixel.io/resume-intent": i.Digest()}}
	}
	for _, suffix := range []string{"workspace", "state"} {
		v := map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": meta(name + "-" + suffix), "spec": map[string]any{"storageClassName": "local-path", "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": "256Mi"}}}}
		if err := b.ensure(ctx, "pvc", v, i); err != nil {
			return err
		}
	}
	v := map[string]any{"apiVersion": "agents.x-k8s.io/v1beta1", "kind": "Sandbox", "metadata": meta(name), "spec": map[string]any{"operatingMode": "Running", "service": false, "shutdownTime": i.Deadline.Add(15 * time.Minute).UTC().Format(time.RFC3339), "shutdownPolicy": "Retain", "podTemplate": map[string]any{"spec": b.spec(name)}}}
	return b.ensure(ctx, "sandbox", v, i)
}

type compute struct{ Sandbox, Pod, Workspace, State, Scratch, ScratchVolume string }

func (b *Bundle) inspect(ctx context.Context, i sessions.ResumeIntent) (compute, error) {
	var c compute
	if err := b.namespace(ctx); err != nil {
		return c, err
	}
	name := candidateName(i)
	sb, err := b.get(ctx, "sandbox", name)
	if err != nil {
		return c, err
	}
	if !owned(sb, i) || metadata(sb)["deletionTimestamp"] != nil {
		return c, workspace.ErrIntegrity
	}
	c.Sandbox = uid(sb)
	p, err := b.get(ctx, "pod", name)
	if err != nil {
		return c, err
	}
	if p == nil {
		return c, workspace.ErrUnavailable
	}
	c.Pod = uid(p)
	owners, _ := metadata(p)["ownerReferences"].([]any)
	owner := false
	for _, o := range owners {
		m, _ := o.(map[string]any)
		if m["uid"] == c.Sandbox && m["controller"] == true {
			owner = true
		}
	}
	spec, _ := p["spec"].(map[string]any)
	if !owner || c.Pod == "" || metadata(p)["deletionTimestamp"] != nil || !subset(normalized(b.spec(name)), spec) {
		return c, workspace.ErrIntegrity
	}
	// Defaulted fields may be present, but no additional way to inject execution,
	// secrets, host access or a second writer is allowed in the effective Pod.
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "shareProcessNamespace"} {
		if spec[key] != nil && spec[key] != false {
			return c, workspace.ErrIntegrity
		}
	}
	for _, key := range []string{"initContainers", "ephemeralContainers", "imagePullSecrets"} {
		if a, _ := spec[key].([]any); len(a) != 0 {
			return c, workspace.ErrIntegrity
		}
	}
	containers, _ := spec["containers"].([]any)
	if len(containers) != 1 {
		return c, workspace.ErrIntegrity
	}
	container, _ := containers[0].(map[string]any)
	for _, key := range []string{"env", "envFrom", "volumeDevices", "ports", "args"} {
		if a, _ := container[key].([]any); len(a) != 0 {
			return c, workspace.ErrIntegrity
		}
	}
	if container["lifecycle"] != nil || container["livenessProbe"] != nil || container["readinessProbe"] != nil || container["startupProbe"] != nil {
		return c, workspace.ErrIntegrity
	}
	status, _ := p["status"].(map[string]any)
	if status["phase"] != "Running" {
		return c, workspace.ErrUnavailable
	}
	for suffix, target := range map[string]*string{"workspace": &c.Workspace, "state": &c.State} {
		v, e := b.get(ctx, "pvc", name+"-"+suffix)
		if e != nil {
			return c, e
		}
		if !owned(v, i) || metadata(v)["deletionTimestamp"] != nil {
			return c, workspace.ErrIntegrity
		}
		*target = uid(v)
	}
	c.Scratch, c.ScratchVolume, err = b.scratch(ctx, i, c.Pod)
	if err != nil {
		return c, err
	}
	return c, nil
}

func (b *Bundle) scratch(ctx context.Context, i sessions.ResumeIntent, podUID string) (string, string, error) {
	claim, err := b.get(ctx, "pvc", candidateName(i)+"-scratch")
	if err != nil {
		return "", "", err
	}
	spec, _ := claim["spec"].(map[string]any)
	status, _ := claim["status"].(map[string]any)
	owners, _ := metadata(claim)["ownerReferences"].([]any)
	owned := false
	for _, v := range owners {
		m, _ := v.(map[string]any)
		if m["uid"] == podUID && m["controller"] == true {
			owned = true
		}
	}
	capacity, _ := status["capacity"].(map[string]any)
	name, _ := spec["volumeName"].(string)
	if !owned || uid(claim) == "" || name == "" || status["phase"] != "Bound" || spec["storageClassName"] != "ar-bounded-scratch-v1" || capacity["storage"] != "32Mi" {
		return "", "", workspace.ErrIntegrity
	}
	pv, err := b.get(ctx, "pv", name)
	if err != nil {
		return "", "", err
	}
	ps, _ := pv["spec"].(map[string]any)
	ref, _ := ps["claimRef"].(map[string]any)
	size, _ := ps["capacity"].(map[string]any)
	local, _ := ps["local"].(map[string]any)
	path, _ := local["path"].(string)
	if uid(pv) == "" || ref["uid"] != uid(claim) || size["storage"] != "32Mi" || ps["storageClassName"] != "ar-bounded-scratch-v1" || !strings.HasPrefix(path, "/mnt/ssd/thinkpixelar-scratch/ar-scratch-") || filepath.Clean(path) != path {
		return "", "", workspace.ErrIntegrity
	}
	// Inspect the actual host filesystem, independently of the guest and its
	// claim size. The operator-prepared class uses fresh bounded ext4 filesystems.
	script := []byte("import json,os,subprocess,sys\np=sys.argv[1]\nf=json.loads(subprocess.check_output(['findmnt','-J','-T',p]))['filesystems'][0]\ns=os.statvfs(p)\nassert f['target']==p and f['fstype']=='ext4' and 16777216<=s.f_blocks*s.f_frsize<=33554432\nprint('bounded')\n")
	out, err := remote(ctx, "pi@10.10.10.12", script, "sudo", "python3", "-", path)
	if err != nil || strings.TrimSpace(string(out)) != "bounded" {
		return "", "", workspace.ErrIntegrity
	}
	return uid(claim), uid(pv), nil
}
func (b *Bundle) hostProof(ctx context.Context, i sessions.ResumeIntent, c compute) error {
	script, err := ReadFile(b.config.ProofScript, 64<<10)
	if err != nil || sandbox.Digest(script) != b.config.ProofScriptDigest {
		return workspace.ErrIntegrity
	}
	raw, err := remote(ctx, "pi@10.10.10.12", script, "sudo", "python3", "-", "--name", candidateName(i), "--namespace", b.config.Namespace, "--uid", c.Pod, "--handler", "kata-qemu-runtime-rs-ar331-bounded")
	if err != nil {
		return err
	}
	var proof struct {
		KVM     bool   `json:"kvm_vm_descriptor"`
		Pod     string `json:"pod_uid"`
		Handler string `json:"handler"`
	}
	if json.Unmarshal(raw, &proof) != nil || !proof.KVM || proof.Pod != c.Pod || proof.Handler != "kata-qemu-runtime-rs-ar331-bounded" {
		return workspace.ErrIntegrity
	}
	return nil
}
func probeConfig(i sessions.ResumeIntent) agentd.Config {
	digest := "sha256:" + codex.LinuxARM64SHA256
	return agentd.Config{Version: 1, Endpoint: "https://resume.invalid", ServerName: "resume.invalid", Binding: &agentdv1.Binding{TenantId: string(i.Request.Caller.TenantID), SessionId: string(i.Request.SessionID), ExecutionId: string(i.BootstrapID), AttemptId: string(i.BootstrapID), SandboxBindingId: string(i.SandboxID), SessionGeneration: max(1, uint64(i.ExecutionGeneration))}, BuildDigest: digest, AdapterKind: codex.Kind, AdapterDigest: digest, Protocol: &agentdv1.VersionRange{Major: 1}, Capabilities: []string{"envelope.v1", control.ThreadCapability}, RequiredCapabilities: []string{"envelope.v1", control.ThreadCapability}, Limits: &agentdv1.Limits{FrameBytes: 1048576, CommandBytes: 262144, EventBytes: 262144, DiagnosticBytes: 16384, MutatingInflight: 1, ReadonlyInflight: 32, BufferedEvents: 256, BufferedBytes: 16777216, HeartbeatIntervalMs: 10000, LivenessWindowMs: 30000}, Harness: agentd.HarnessConfig{Argv: codex.Command(), WorkingDirectory: "/workspace", StartTimeoutMS: 10000, StopGraceMS: 5000, KillWaitMS: 1000}}
}
func (b *Bundle) probe(ctx context.Context, i sessions.ResumeIntent) (agentd.ResumeProof, error) {
	var proof agentd.ResumeProof
	work, err := b.object("workspace")
	if err != nil {
		return proof, err
	}
	rollout, err := b.object("rollout")
	if err != nil {
		return proof, err
	}
	metadata, err := b.object("restore")
	if err != nil {
		return proof, err
	}
	in := agentd.ResumeProbe{Config: probeConfig(i), Workspace: work, Rollout: rollout, Metadata: metadata}
	raw, _ := json.Marshal(in)
	out, err := b.command(ctx, raw, "exec", "-i", candidateName(i), "--", "/usr/local/bin/thinkpixel-resume-probe")
	if err != nil {
		return proof, err
	}
	var selected codex.RestoreState
	if json.Unmarshal(out, &proof) != nil || json.Unmarshal(metadata, &selected) != nil || proof.Thread != selected.ThreadID || "sha256:"+proof.WorkspaceSHA256 != sandbox.Digest(work) || "sha256:"+proof.RolloutSHA256 != sandbox.Digest(rollout) {
		return proof, workspace.ErrIntegrity
	}
	return proof, nil
}
func (b *Bundle) observation(i sessions.ResumeIntent, c compute, p agentd.ResumeProof) sessions.ResumeObservation {
	raw, _ := json.Marshal(struct {
		Compute compute
		Proof   agentd.ResumeProof
	}{c, p})
	return sessions.ResumeObservation{SandboxReference: b.config.Namespace + "/" + candidateName(i) + "/" + c.Sandbox, AttachmentReference: b.config.Namespace + "/" + c.Workspace + "/" + c.State + "/" + string(i.AttachmentID), EvidenceDigest: sandbox.Digest(raw)}
}
