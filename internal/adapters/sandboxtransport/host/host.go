// Package agentdhost composes the optional standalone AR transport listener.
// It only controls already admitted and materialized local Executions.
package agentdhost

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	kube "github.com/bdobrica/ThinkPixelAR/internal/adapters/kubernetes"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandbox/agentsandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	grpctransport "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/grpc"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/localissuer"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdbootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdserver"
	"github.com/bdobrica/ThinkPixelAR/internal/app/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
	v1 "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

const ConfigEnvironment = "THINKPIXELAR_AGENTD_CONFIG_FILE"

var ErrHost = errors.New("agentd listener configuration or startup rejected")

type Binding struct {
	TenantID, SandboxID primitives.ID
	RequestDigest       string
	Blueprint           core.SandboxBlueprint
	NetworkPolicy       *networking.NetworkPolicy
	Commands            []agentdserver.Command
}

// Config is trusted operator input, never an HTTP or sandbox request. TLS and
// evidence files must be on AR-only mounts. There are no generated CA defaults.
type Config struct {
	Version                                                int
	Mode, Revision, ListenAddress, ServerName, TrustDomain string
	ServerCertificate, ServerKey, ClientCA, ClientCAKey    string
	EvidenceDirectory                                      string
	Homelab                                                agentsandbox.HomelabPin
	Discovery                                              agentsandbox.DiscoveryPin
	CapabilityDigest                                       string
	Tenants                                                []primitives.ID
	Bindings                                               []Binding
}

type Host struct {
	listener net.Listener
	server   *grpctransport.Server
	worker   *agentdbootstrap.Worker
	db       *sql.DB
	evidence *agentsandbox.HomelabVerifier
	maintain func(context.Context)
}

func read(path string, limit int64, private bool) ([]byte, error) {
	// The parent is operator-controlled; reject a named pipe/device before an
	// open can block startup. Recheck the opened regular file and permissions.
	before, err := os.Stat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, ErrHost
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrHost
	}
	defer func() { _ = f.Close() }()
	i, err := f.Stat()
	if err != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 || i.Size() > limit || private && i.Mode().Perm()&0077 != 0 {
		return nil, ErrHost
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		clear(b)
		return nil, ErrHost
	}
	return b, nil
}

func Load(path string) (Config, error) {
	var c Config
	b, err := read(path, 1<<20, false)
	if err != nil {
		return c, ErrHost
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.Mode != "local" || len(c.Bindings) == 0 || len(c.Bindings) > 128 || len(c.Tenants) == 0 || len(c.Tenants) > 128 {
		return Config{}, ErrHost
	}
	seen := map[primitives.ID]bool{}
	for _, t := range c.Tenants {
		if _, err := primitives.ParseID(string(t)); err != nil || seen[t] {
			return Config{}, ErrHost
		}
		seen[t] = true
	}
	scopes := map[string]bool{}
	for _, b := range c.Bindings {
		key := string(b.TenantID) + "/" + string(b.SandboxID)
		if _, err := primitives.ParseID(string(b.SandboxID)); err != nil || !seen[b.TenantID] || scopes[key] || b.NetworkPolicy == nil || len(b.Commands) > 128 {
			return Config{}, ErrHost
		}
		scopes[key] = true
	}
	if _, _, err = net.SplitHostPort(c.ListenAddress); err != nil {
		return Config{}, ErrHost
	}
	return c, nil
}

// Open validates all dependencies before opening a listener. It never migrates,
// admits an Execution, registers a materialization or issues bootstrap on restart.
func Open(ctx context.Context, path string, database config.Secret, kc config.Kubernetes, logger *slog.Logger) (_ *Host, err error) {
	if logger == nil {
		return nil, ErrHost
	}
	c, err := Load(path)
	if err != nil {
		return nil, ErrHost
	}
	h := &Host{}
	defer func() {
		if err != nil {
			h.Close()
			err = ErrHost
		}
	}()
	h.db, err = sql.Open("pgx", database.Reveal())
	if err != nil {
		return nil, err
	}
	h.db.SetMaxOpenConns(8)
	h.db.SetMaxIdleConns(4)
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = h.db.PingContext(check); err != nil {
		return nil, err
	}
	conn, err := kube.Connect(kc)
	if err != nil {
		return nil, err
	}
	bindings, err := postgres.NewSandboxBindings(h.db)
	if err != nil {
		return nil, err
	}
	registry, err := postgres.NewAgentdCredentials(h.db)
	if err != nil {
		return nil, err
	}
	policy, err := postgres.NewAgentdLocalPolicy(h.db, c.Mode, c.Revision, "thinkpixelar/local")
	if err != nil {
		return nil, err
	}
	h.evidence, err = agentsandbox.NewHomelabVerifier(conn.Client, c.EvidenceDirectory, c.Homelab)
	if err != nil {
		return nil, err
	}
	lookup := func(r sandbox.AcquireRequest) (Binding, error) {
		digest, e := sandbox.RequestDigest(r)
		if e != nil || digest != r.Operation.Digest {
			return Binding{}, ErrHost
		}
		for _, b := range c.Bindings {
			if b.TenantID == r.Scope.TenantID && b.SandboxID == r.Scope.SandboxID && b.RequestDigest == digest {
				return b, nil
			}
		}
		return Binding{}, ErrHost
	}
	resolve := func(_ context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		b, e := lookup(r)
		if e != nil {
			return core.SandboxBlueprint{}, e
		}
		return *b.Blueprint.DeepCopy(), nil
	}
	verify, err := agentsandbox.NewSecureEffectiveVerifier(resolve, h.evidence.Verify)
	if err != nil {
		return nil, err
	}
	discovery, err := agentsandbox.NewCapabilityDiscovery(conn.Discovery, conn.Client, c.Discovery)
	if err != nil {
		return nil, err
	}
	network := func(ctx context.Context, r sandbox.AcquireRequest, namespace string) error {
		b, e := lookup(r)
		if e != nil {
			return e
		}
		enforce, e := agentsandbox.NewNamespaceNetworkEnforcer(conn.Client, agentsandbox.NamespaceNetworkBinding{ProfileDigest: r.ProfileDigest, ImplementationDigest: r.ImplementationDigest, Policy: b.NetworkPolicy}, func(ctx context.Context, _ sandbox.AcquireRequest, _ *networking.NetworkPolicy) error {
			binding, e := bindings.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID)
			if e != nil {
				return e
			}
			u, e := conn.Client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(namespace).Get(ctx, "ar-"+string(r.Scope.SandboxID), metav1.GetOptions{})
			if e != nil {
				return ErrHost
			}
			var pod v1.Pod
			if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &pod) != nil {
				return ErrHost
			}
			return h.evidence.Verify(ctx, binding, &pod)
		})
		if e != nil {
			return e
		}
		return enforce(ctx, r, namespace)
	}
	provider, err := agentsandbox.New(conn.Client, bindings, conn.Namespace, resolve, agentsandbox.WithEffectiveVerifier(verify), agentsandbox.WithCapabilities(discovery, c.CapabilityDigest), agentsandbox.WithNetworkEnforcer(network))
	if err != nil {
		return nil, err
	}
	compute, err := reconciliation.NewCompute(bindings, provider, cleanupOnly{})
	if err != nil {
		return nil, err
	}
	h.maintain = func(ctx context.Context) {
		for _, b := range c.Bindings {
			if ctx.Err() != nil {
				return
			}
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			e := bindings.RecoverExpiredAgentd(call, b.TenantID, b.SandboxID)
			if e == nil {
				var intent sandbox.ComputeIntent
				intent, e = bindings.LoadCompute(call, b.TenantID, b.SandboxID)
				if e == nil && intent.Desired == sandbox.ComputeReleased {
					_, e = compute.Reconcile(call, b.TenantID, b.SandboxID)
				}
			}
			cancel()
			if e != nil {
				logger.Warn("agentd recovery cleanup deferred", "sandbox_id", b.SandboxID)
			}
		}
	}
	admission, err := agentdadmission.New(bindings, provider, registry, policy, policy)
	if err != nil {
		return nil, err
	}
	cert, err := read(c.ServerCertificate, 32768, false)
	if err != nil {
		return nil, err
	}
	defer clear(cert)
	key, err := read(c.ServerKey, 32768, true)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	serverCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	ca, err := read(c.ClientCA, 32768, false)
	if err != nil {
		return nil, err
	}
	defer clear(ca)
	caKey, err := read(c.ClientCAKey, 32768, true)
	if err != nil {
		return nil, err
	}
	defer clear(caKey)
	issuerPair, err := tls.X509KeyPair(ca, caKey)
	if err != nil {
		return nil, err
	}
	signer, ok := issuerPair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, ErrHost
	}
	block, rest := pem.Decode(ca)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrHost
	}
	issuer, err := localissuer.New(block.Bytes, signer, c.TrustDomain)
	if err != nil {
		return nil, err
	}
	identity, err := agentdidentity.New(issuer, admission)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, ErrHost
	}
	secrets, err := bootstrap.New(conn.Client, conn.Namespace, c.TrustDomain, registry)
	if err != nil {
		return nil, err
	}
	journal, err := postgres.NewAgentdDelivery(h.db)
	if err != nil {
		return nil, err
	}
	delivery, err := bootstrap.NewDelivery(secrets, journal, func(ctx context.Context, r transport.CredentialRecord) error {
		_, e := admission.AuthorizeCredential(ctx, transport.CredentialRequest{Identity: r.Identity})
		return e
	})
	if err != nil {
		return nil, err
	}
	h.worker, err = agentdbootstrap.NewWorker(delivery, c.Tenants, 5*time.Second, 5*time.Second, 32, logger)
	if err != nil {
		return nil, err
	}
	handler := agentdserver.Handler{Rotation: identity, Outcomes: policy, Plan: func(ctx context.Context, peer transport.Peer) ([]agentdserver.Command, error) {
		ready, err := registry.IsSessionCredential(ctx, peer)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, nil
		}
		id := peer.Identity
		for _, b := range c.Bindings {
			if b.TenantID == id.TenantID && b.SandboxID == id.SandboxID {
				return slices.Clone(b.Commands), nil
			}
		}
		return nil, ErrHost
	}, Observe: func(_ context.Context, f *agentdv1.Envelope) error {
		if control.IsShutdownObservation(f) {
			logger.Info("agentd final stop observation", "sandbox_id", f.Binding.SandboxBindingId, "connection_epoch", f.ConnectionEpoch, "managed_process_stopped", string(f.GetObservation().Payload) == control.ShutdownStopped)
		}
		return nil
	}}
	h.server, err = grpctransport.NewServer(grpctransport.ServerConfig{Certificate: serverCert, ServerName: c.ServerName, TrustDomain: c.TrustDomain, ClientRoots: roots, Authorizer: admission, Handle: handler.Serve, MaxConnections: 8})
	if err != nil {
		return nil, err
	}
	h.listener, err = net.Listen("tcp", c.ListenAddress)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Host) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.worker.Run(ctx) }()
	maintenance := make(chan struct{})
	go func() {
		defer close(maintenance)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			h.maintain(ctx)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	err := h.server.Serve(ctx, h.listener)
	cancel()
	workerErr := <-done
	<-maintenance
	if err != nil || workerErr != nil {
		return ErrHost
	}
	return nil
}

func (h *Host) Close() {
	if h.listener != nil {
		_ = h.listener.Close()
	}
	if h.evidence != nil {
		_ = h.evidence.Close()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
}

// This host only drains exact persisted release intents. Recovery must not
// accidentally turn a monitoring pass into acquisition or execution authority.
type cleanupOnly struct{}

func (cleanupOnly) CheckCompute(context.Context, sandbox.Scope) error { return sandbox.ErrPermission }
