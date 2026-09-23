package postgres_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdbootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdserver"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Opt-in: real PostgreSQL, mTLS, production agentd image, and a real child.
// Only external Kubernetes readiness and Secret API operations are fixtures.
func TestAgentdIntegratedAcceptance(t *testing.T) {
	image := os.Getenv("THINKPIXELAR_TEST_AGENTD_IMAGE")
	if image == "" {
		t.Skip("THINKPIXELAR_TEST_AGENTD_IMAGE is not set")
	}
	if os.Getenv("THINKPIXELAR_TEST_DATABASE_URL") == "" {
		t.Fatal("acceptance requires THINKPIXELAR_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v (%s)", name, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	image = run("docker", "image", "inspect", "--format", "{{.Id}}", image)
	t.Log("agentd image:", image)
	root := t.TempDir()
	workspace, bundle := filepath.Join(root, "workspace"), filepath.Join(root, "bootstrap")
	for _, dir := range []string{workspace, bundle} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(workspace, 0777); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(root, "harness")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", harness, "../../../test/harnessfixture/cmd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v (%s)", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	db, policy, m, intent, _ := policyFixtureConfigured(t, true, func(c *agentd.Config) {
		c.Endpoint = "https://localhost:" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
		c.ServerName = "localhost"
		c.Harness.Argv = []string{"/usr/local/bin/test-harness", "/workspace/fixture.sock"}
	})
	scope := intent.Binding.Request.Scope
	id := transport.Identity{TenantID: scope.TenantID, SandboxID: scope.SandboxID, AttemptID: scope.AttemptID}
	registry, _ := postgres.NewAgentdCredentials(db)
	bindings, _ := postgres.NewSandboxBindings(db)
	admission, err := agentdadmission.New(bindings, &admissionFixture{r: intent.Binding.Request}, registry, policy, policy)
	if err != nil {
		t.Fatal(err)
	}
	clientCA, clientKey := bootstrapCA(t, x509.ExtKeyUsageClientAuth, time.Hour)
	serverCA, serverKey := bootstrapCA(t, x509.ExtKeyUsageServerAuth, time.Hour)
	issuer, err := localissuer.New(clientCA, clientKey, "ar.test")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := agentdidentity.New(issuer, admission)
	if err != nil {
		t.Fatal(err)
	}
	api := &bootstrapTestClient{objects: map[string]*unstructured.Unstructured{}}
	secrets, err := bootstrap.New(api, "sandboxes", "ar.test", registry)
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := postgres.NewAgentdDelivery(db)
	delivery, err := bootstrap.NewDelivery(secrets, journal, func(c context.Context, r transport.CredentialRecord) error {
		_, e := admission.AuthorizeCredential(c, transport.CredentialRequest{Identity: r.Identity})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := agentdbootstrap.New(identity, delivery)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(m.Config)
	if err != nil {
		t.Fatal(err)
	}
	var reference bootstrap.Reference
	err = materializer.Materialize(ctx, id, config, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCA}), m.Challenge, func(c context.Context, name string) error {
		cid, e := primitives.ParseID(strings.TrimPrefix(name, "agentd-"))
		if e != nil {
			return e
		}
		entry, e := journal.LoadDelivery(c, id.TenantID, cid)
		if e != nil {
			return e
		}
		if entry.Reference.UID == "" {
			return fmt.Errorf("projection preceded durable UID")
		}
		reference = entry.Reference
		obj, e := api.Resource(bootstrapTestSecrets).Namespace("sandboxes").Get(c, name, metav1.GetOptions{})
		if e != nil {
			return e
		}
		raw, e := json.Marshal(obj.Object)
		if e != nil {
			return e
		}
		var secret struct {
			Data map[string][]byte `json:"data"`
		}
		if e = json.Unmarshal(raw, &secret); e != nil {
			return e
		}
		for name, data := range secret.Data {
			if e = os.WriteFile(filepath.Join(bundle, name), data, 0444); e != nil {
				return e
			}
			clear(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("bootstrap: issued, journaled, UID persisted, projected")

	newID := func() string {
		v, e := primitives.NewID(time.Now())
		if e != nil {
			t.Fatal(e)
		}
		return string(v)
	}
	digest := agentd.ConfigurationDigest(m.Config)
	command := func(kind agentdv1.Command_Kind) agentdserver.Command {
		return agentdserver.Command{OperationID: newID(), ConfigurationDigest: digest, HarnessHandle: m.HarnessHandle, Kind: kind, Deadline: m.Deadline}
	}
	start, status, interrupt, after := command(agentdv1.Command_START), command(agentdv1.Command_STATUS), command(agentdv1.Command_INTERRUPT), command(agentdv1.Command_STATUS)
	staleProbe := command(agentdv1.Command_STATUS)
	var phase atomic.Int32
	var captured, running, exited, final atomic.Bool
	sessions := make(chan *grpctransport.Session, 8)
	negative := make(chan error, 2)
	var oldConnection transport.Connection // Set before publishing phase 1.
	frame := func(c agentdserver.Command, connection transport.Connection) *agentdv1.Envelope {
		mid, _ := primitives.NewID(time.Now())
		return &agentdv1.Envelope{Major: 1, Binding: m.Config.Binding, ConnectionId: string(connection.ID), ConnectionEpoch: connection.Epoch, MessageId: string(mid), Sequence: 1, OperationId: c.OperationID, HarnessHandle: c.HarnessHandle, RequestDigest: control.Digest(c.Kind, digest, c.HarnessHandle), SentUnixMs: time.Now().UnixMilli(), DeadlineUnixMs: c.Deadline.UnixMilli(), Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: c.Kind, ConfigurationDigest: digest, PayloadSchema: control.Capability}}}
	}
	handler := agentdserver.Handler{Rotation: identity, Outcomes: policy, Plan: func(c context.Context, p transport.Peer) ([]agentdserver.Command, error) {
		ready, e := registry.IsSessionCredential(c, p)
		if e != nil || !ready {
			return nil, e
		}
		if phase.Load() == 0 {
			return []agentdserver.Command{start, status, interrupt}, nil
		}
		return []agentdserver.Command{after}, nil
	}, Observe: func(_ context.Context, f *agentdv1.Envelope) error {
		if o := f.GetObservation(); o != nil && o.PayloadSchema == control.Capability+"/stdout" {
			if string(o.Payload) != "[REDACTED]" {
				return fmt.Errorf("unsanitized fixture output")
			}
			captured.Store(true)
		}
		if h := f.GetHeartbeat(); h != nil {
			if phase.Load() > 0 && h.ProcessState == agentdv1.Heartbeat_RUNNING {
				return fmt.Errorf("reconnect restarted interrupted work")
			}
			if h.ProcessState == agentdv1.Heartbeat_RUNNING {
				running.Store(true)
			}
			if h.ProcessState == agentdv1.Heartbeat_EXITED {
				exited.Store(true)
			}
		}
		if control.IsShutdownObservation(f) {
			final.Store(true)
		}
		return nil
	}}
	parent, e := x509.ParseCertificate(serverCA)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, leaf, parent, &key.PublicKey, serverKey)
	if e != nil {
		t.Fatal(e)
	}
	clientRoots := x509.NewCertPool()
	ca, e := x509.ParseCertificate(clientCA)
	if e != nil {
		t.Fatal(e)
	}
	clientRoots.AddCert(ca)
	server, e := grpctransport.NewServer(grpctransport.ServerConfig{Certificate: tls.Certificate{Certificate: [][]byte{der, serverCA}, PrivateKey: key}, ClientRoots: clientRoots, ServerName: "localhost", TrustDomain: "ar.test", MaxConnections: 4, Authorizer: admission, Handle: func(c context.Context, s *grpctransport.Session) error {
		w := s.Welcome()
		current := transport.Connection{ID: primitives.ID(w.ConnectionId), Epoch: w.ConnectionEpoch, Deadline: m.Deadline}
		switch phase.Load() {
		case 1:
			phase.Store(2)
			if s.Send(frame(start, current)) == nil {
				negative <- fmt.Errorf("duplicate delivered")
			} else {
				negative <- nil
			}
			return nil
		case 2:
			phase.Store(3)
			// A fresh operation isolates epoch rejection from duplicate rejection.
			stale := frame(staleProbe, oldConnection)
			if current.Epoch <= oldConnection.Epoch || policy.AuthorizeFrame(c, intent, oldConnection, stale) == nil || s.Send(stale) == nil {
				negative <- fmt.Errorf("stale epoch admitted")
			} else {
				negative <- nil
			}
			return nil
		default:
			select {
			case sessions <- s:
			case <-c.Done():
				return nil
			}
			return handler.Serve(c, s)
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(ctx, listener) }()
	defer func() {
		cancel()
		if e := <-serverDone; e != nil {
			t.Error(e)
		}
	}()
	name := "ar-agentd-acceptance-" + string(scope.SandboxID)
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if out, e := exec.CommandContext(c, "docker", "rm", "-f", name).CombinedOutput(); e != nil {
			t.Errorf("test container cleanup: %v (%s)", e, out)
		}
	})
	run("docker", "run", "-d", "--name", name, "--network", "host", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m", "--cpus", "1", "--mount", "type=bind,src="+bundle+",dst=/run/thinkpixel/bootstrap,readonly", "--mount", "type=bind,src="+harness+",dst=/usr/local/bin/test-harness,readonly", "--mount", "type=bind,src="+workspace+",dst=/workspace", image)
	await := func(label string, check func() bool) {
		t.Helper()
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if check() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(label + ": context expired")
			case <-deadline.C:
				t.Fatal(label + ": timed out")
			case <-tick.C:
			}
		}
	}
	outcome := func(c agentdserver.Command) bool {
		v, e := policy.CommandOutcome(ctx, scope.TenantID, scope.SandboxID, primitives.ID(c.OperationID), control.Digest(c.Kind, digest, c.HarnessHandle))
		return e == nil && v == transport.DispatchAcknowledged
	}
	await("capture and running status", func() bool { return captured.Load() && running.Load() && outcome(start) })
	// Run the protocol probe as the same unprivileged container user: the
	// fixture socket is private (0600), just as in a sandbox.
	run("docker", "exec", name, "/usr/local/bin/test-harness", "--handshake", "/workspace/fixture.sock")
	t.Log("authenticate/rotate/start/handshake/capture: real mTLS, child and redacted stdout")
	await("status and interrupt", func() bool { return outcome(status) && outcome(interrupt) && exited.Load() })
	// Reconstruct durable access before reconnect; no in-memory command outcome substitute.
	restartedPolicy, e := postgres.NewAgentdLocalPolicy(db, "local", m.Revision, "thinkpixelar/local")
	if e != nil {
		t.Fatal(e)
	}
	if result, e := restartedPolicy.CommandOutcome(ctx, scope.TenantID, scope.SandboxID, primitives.ID(interrupt.OperationID), control.Digest(interrupt.Kind, digest, interrupt.HarnessHandle)); e != nil || result != transport.DispatchAcknowledged {
		t.Fatal("outcome lost on reconstruction", e)
	}
	var active *grpctransport.Session
	for len(sessions) > 0 {
		active = <-sessions
	}
	if active == nil {
		t.Fatal("missing admitted session")
	}
	w := active.Welcome()
	oldConnection = transport.Connection{ID: primitives.ID(w.ConnectionId), Epoch: w.ConnectionEpoch, Deadline: m.Deadline}
	phase.Store(1)
	active.Close()
	for range 2 {
		select {
		case e := <-negative:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("negative reconnect checks timed out")
		}
	}
	await("post-reconnect status", func() bool { return outcome(after) })
	t.Log("interrupt/disconnect/reconnect: persisted outcomes retained; duplicate and stale commands rejected")
	var commandCount int
	if e = db.QueryRow(`SELECT count(*) FROM agentd_commands WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID).Scan(&commandCount); e != nil || commandCount != 4 {
		t.Fatal("unexpected command replay", commandCount, e)
	}
	worker, e := agentdbootstrap.NewWorker(delivery, []primitives.ID{scope.TenantID}, 5*time.Second, time.Second, 8, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	defer func() {
		stopWorker()
		if e := <-workerDone; e != nil {
			t.Error(e)
		}
	}()
	await("bootstrap cleanup", func() bool {
		entry, e := journal.LoadDelivery(ctx, scope.TenantID, reference.Record.CredentialID)
		return e == nil && entry.Cleaned
	})
	if _, e = api.Resource(bootstrapTestSecrets).Namespace("sandboxes").Get(ctx, reference.Name, metav1.GetOptions{}); !apierrors.IsNotFound(e) {
		t.Fatal("bootstrap Secret retained", e)
	}
	run("docker", "stop", "--time", "5", name)
	if code := run("docker", "inspect", "--format", "{{.State.ExitCode}}", name); code != "0" {
		t.Fatal("agentd shutdown exit", code)
	}
	if !final.Load() {
		t.Fatal("missing authenticated final report")
	}
	if e = policy.Revoke(ctx, scope.TenantID, scope.SandboxID); e != nil {
		t.Fatal(e)
	}
	if _, e = policy.AuthorizeTransport(ctx, intent, transport.AcceptStream); e == nil {
		t.Fatal("revoked materialization accepted")
	}
	t.Log("cleanup: durable Secret deletion, bounded supervisor exit, final report and revocation confirmed")
}
