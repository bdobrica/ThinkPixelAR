package kubernetes

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestConnectionModesAndSanitizedErrors(t *testing.T) {
	for _, mode := range []string{"in-cluster", "development"} {
		t.Run(mode, func(t *testing.T) {
			c := config.Kubernetes{Mode: mode, Namespace: "sandbox", APITimeout: time.Second}
			if mode == "development" {
				c.Kubeconfig = "explicit"
				c.Context = "selected"
			}
			called := 0
			loader := func() (*rest.Config, error) { called++; return &rest.Config{Host: "https://cluster.invalid"}, nil }
			dev := func(path, ctx string) (*rest.Config, error) {
				if path != "explicit" || ctx != "selected" {
					t.Fatal("configuration not forwarded")
				}
				return loader()
			}
			if _, err := connect(c, loader, dev); err != nil || called != 1 {
				t.Fatalf("connect: %v, calls %d", err, called)
			}
		})
	}
	c := config.Kubernetes{Mode: "in-cluster", Namespace: "sandbox", APITimeout: time.Second}
	_, err := connect(c, func() (*rest.Config, error) { return nil, errors.New("sensitive credential") }, nil)
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("raw loader error escaped")
	}
	for _, rc := range []*rest.Config{{Host: "http://cluster"}, {Host: "https://cluster", TLSClientConfig: rest.TLSClientConfig{Insecure: true}}} {
		if _, err := connect(c, func() (*rest.Config, error) { return rc, nil }, nil); err == nil {
			t.Fatal("insecure endpoint accepted")
		}
	}
}

func TestAPIRequestIsBounded(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	rc := &rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}}
	c := config.Kubernetes{Mode: "in-cluster", Namespace: "sandbox", APITimeout: 50 * time.Millisecond}
	conn, err := connect(c, func() (*rest.Config, error) { return rc, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = conn.Client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(conn.Namespace).Get(context.Background(), "test", metav1.GetOptions{})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("unbounded request: %v", err)
	}
	if rc.Timeout != 0 {
		t.Fatal("mutated source config")
	}
}
