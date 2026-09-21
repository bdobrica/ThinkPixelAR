// Package kubernetes owns Kubernetes connections shared by infrastructure
// adapters. Kubernetes types must not cross into domain or application ports.
package kubernetes

import (
	"errors"
	"net/url"

	"github.com/bdobrica/ThinkPixelAR/internal/config"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client is an adapter-local port. Domain code depends on SandboxProvider,
// never this client or raw Kubernetes objects.
type Client interface{ dynamic.Interface }

type Connection struct {
	Client    Client
	Discovery rest.Interface
	Namespace string
}

// Connect loads credentials only in the trusted control plane. Construction
// performs no API calls; every later request has a hard HTTP timeout and may
// additionally be shortened through its context.
func Connect(c config.Kubernetes) (Connection, error) {
	return connect(c, rest.InClusterConfig, func(path, context string) (*rest.Config, error) {
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
			&clientcmd.ConfigOverrides{CurrentContext: context}).ClientConfig()
	})
}

func connect(c config.Kubernetes, inCluster func() (*rest.Config, error), development func(string, string) (*rest.Config, error)) (Connection, error) {
	if err := c.Validate(); err != nil {
		return Connection{}, err
	}
	var rc *rest.Config
	var err error
	if c.Mode == "in-cluster" {
		rc, err = inCluster()
	} else {
		rc, err = development(c.Kubeconfig, c.Context)
	}
	if err != nil || rc == nil {
		return Connection{}, errors.New("kubernetes credentials unavailable")
	}
	rc = rest.CopyConfig(rc)
	endpoint, err := url.Parse(rc.Host)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || rc.Insecure {
		return Connection{}, errors.New("kubernetes requires a verified HTTPS endpoint")
	}
	rc.Timeout = c.APITimeout
	rc.QPS, rc.Burst = 10, 20
	rc.UserAgent = "thinkpixelar"
	client, err := dynamic.NewForConfig(rc)
	if err != nil {
		return Connection{}, errors.New("kubernetes client initialization failed")
	}
	discoveryClient, err := rest.UnversionedRESTClientFor(dynamic.ConfigFor(rc))
	if err != nil {
		return Connection{}, errors.New("kubernetes discovery initialization failed")
	}
	return Connection{Client: client, Discovery: discoveryClient, Namespace: c.Namespace}, nil
}
