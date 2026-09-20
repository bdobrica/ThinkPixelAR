package agentsandbox

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestPinnedAPIs(t *testing.T) {
	s, err := apiScheme()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ resource, kind string }{{sandboxResource.String(), "Sandbox"}, {templateResource.String(), "SandboxTemplate"}, {claimResource.String(), "SandboxClaim"}} {
		if !strings.Contains(r.resource, "v1beta1") {
			t.Fatalf("wrong API: %s", r.resource)
		}
		found := false
		for gvk := range s.AllKnownTypes() {
			if gvk.Kind == r.kind && gvk.Version == "v1beta1" {
				obj, err := s.New(gvk)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = runtime.DefaultUnstructuredConverter.ToUnstructured(obj); err != nil {
					t.Fatal(err)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s", r.kind)
		}
	}
	if upstreamVersion != "v1.0.0" {
		t.Fatal("review version contract on upgrade")
	}
}

func TestKubernetesTypesStayInAdapters(t *testing.T) {
	root := "../../../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(filepath.ToSlash(path), "/internal/adapters/") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, i := range f.Imports {
			name, _ := strconv.Unquote(i.Path.Value)
			if strings.HasPrefix(name, "k8s.io/") || strings.HasPrefix(name, "sigs.k8s.io/agent-sandbox") {
				t.Errorf("infrastructure import outside adapter: %s: %s", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
