package config

import (
	"testing"
	"time"
)

func TestKubernetesConfiguration(t *testing.T) {
	good := map[string]string{"THINKPIXELAR_KUBERNETES_NAMESPACE": "sandbox"}
	lookup := func(k string) (string, bool) { v, ok := good[k]; return v, ok }
	c, err := LoadKubernetes(lookup)
	if err != nil || c.APITimeout != 15*time.Second || c.Mode != "in-cluster" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, bad := range []Kubernetes{
		{Mode: "auto", Namespace: "sandbox", APITimeout: time.Second},
		{Mode: "development", Namespace: "sandbox", APITimeout: time.Second},
		{Mode: "in-cluster", Kubeconfig: "unexpected", Namespace: "sandbox", APITimeout: time.Second},
		{Mode: "in-cluster", Namespace: "sandbox", APITimeout: 0},
		{Mode: "in-cluster", Namespace: "sandbox", APITimeout: 61 * time.Second},
		{Mode: "in-cluster", Namespace: "../all", APITimeout: time.Second},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	good["THINKPIXELAR_KUBERNETES_API_TIMEOUT"] = "secret-invalid"
	if _, err := LoadKubernetes(lookup); err == nil || err.Error() != "invalid Kubernetes API timeout" {
		t.Fatalf("timeout parse: %v", err)
	}
}
