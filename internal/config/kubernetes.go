package config

import (
	"errors"
	"time"
)

// Kubernetes contains only trusted control-plane connection settings. It is
// loaded separately so the HTTP-only service does not require cluster access.
type Kubernetes struct {
	Mode       string
	Kubeconfig string
	Context    string
	Namespace  string
	APITimeout time.Duration
}

// LoadKubernetes deliberately requires an explicit development kubeconfig;
// neither mode silently falls back to another source of cluster credentials.
func LoadKubernetes(lookup func(string) (string, bool)) (Kubernetes, error) {
	c := Kubernetes{Mode: "in-cluster", APITimeout: 15 * time.Second}
	for key, target := range map[string]*string{
		"MODE": &c.Mode, "KUBECONFIG": &c.Kubeconfig, "CONTEXT": &c.Context, "NAMESPACE": &c.Namespace,
	} {
		if v, ok := lookup("THINKPIXELAR_KUBERNETES_" + key); ok {
			*target = v
		}
	}
	if v, ok := lookup("THINKPIXELAR_KUBERNETES_API_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Kubernetes{}, errors.New("invalid Kubernetes API timeout")
		}
		c.APITimeout = d
	}
	return c, c.Validate()
}

func (c Kubernetes) Validate() error {
	if c.Mode != "in-cluster" && c.Mode != "development" {
		return errors.New("invalid Kubernetes configuration mode")
	}
	if c.Mode == "in-cluster" && (c.Kubeconfig != "" || c.Context != "") {
		return errors.New("in-cluster mode forbids kubeconfig overrides")
	}
	if c.Mode == "development" && c.Kubeconfig == "" {
		return errors.New("development mode requires an explicit kubeconfig")
	}
	if c.APITimeout <= 0 || c.APITimeout > time.Minute {
		return errors.New("kubernetes API timeout must be positive and at most one minute")
	}
	if len(c.Namespace) == 0 || len(c.Namespace) > 63 {
		return errors.New("invalid Kubernetes namespace")
	}
	for i, ch := range c.Namespace {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' && i > 0 && i < len(c.Namespace)-1) {
			return errors.New("invalid Kubernetes namespace")
		}
	}
	return nil
}
