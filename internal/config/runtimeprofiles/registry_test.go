package runtimeprofiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/docs/contracts"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../../../docs/profiles/coding-medium-secure.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func accepted(runtimeprofile.Profile) ([]byte, error) {
	return []byte(`{"qualified_tuple":"test-fixture","revision":1}`), nil
}
func registry(t *testing.T) *Registry {
	t.Helper()
	r, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func mutate(t *testing.T, raw []byte, path []string, v any, remove bool) []byte {
	t.Helper()
	var m map[string]any
	if e := json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	cursor := m
	for _, k := range path[:len(path)-1] {
		cursor = cursor[k].(map[string]any)
	}
	if remove {
		delete(cursor, path[len(path)-1])
	} else {
		cursor[path[len(path)-1]] = v
	}
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestRejectEveryRequiredFieldAndUnknownField(t *testing.T) {
	raw := fixture(t)
	r := registry(t)
	var schema map[string]any
	_ = json.Unmarshal([]byte(contracts.RuntimeProfileSchema()), &schema)
	var walk func(map[string]any, []string)
	walk = func(s map[string]any, path []string) {
		if ref, ok := s["$ref"].(string); ok {
			s = schema["$defs"].(map[string]any)[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		}
		if s["type"] != "object" {
			return
		}
		for _, key := range s["required"].([]any) {
			p := append(append([]string{}, path...), key.(string))
			t.Run(strings.Join(p, "."), func(t *testing.T) {
				if r.Reload([][]byte{mutate(t, raw, p, nil, true)}, accepted) == nil {
					t.Fatal("missing required field accepted")
				}
			})
		}
		p := append(append([]string{}, path...), "unexpected")
		if r.Reload([][]byte{mutate(t, raw, p, true, false)}, accepted) == nil {
			t.Fatalf("unknown field accepted at %v", path)
		}
		for k, child := range s["properties"].(map[string]any) {
			walk(child.(map[string]any), append(append([]string{}, path...), k))
		}
	}
	walk(schema, nil)
}

func TestCrossFieldConstraintsAndParsing(t *testing.T) {
	raw := fixture(t)
	r := registry(t)
	cases := []struct {
		path  string
		value any
	}{
		{"resources.cpu_millis.request", 5000}, {"resources.memory_bytes.request", int64(9000000000)},
		{"resources.ephemeral_storage_bytes.request", int64(30000000000)},
		{"resources.gpu.count", 1}, {"resources.gpu.classes", []string{"gpu"}},
		{"security.privileged", true}, {"security.run_as_non_root", false},
		{"security.read_only_root_filesystem", false}, {"security.allow_privilege_escalation", true},
		{"security.service_account_token", true}, {"security.host_network", true},
		{"security.host_pid", true}, {"security.host_ipc", true}, {"security.host_paths", true},
		{"security.runtime_sockets", true}, {"security.linux_capabilities_add", []string{"SYS_ADMIN"}},
		{"network.default_deny_ingress", false}, {"network.default_deny_egress", false},
		{"network.deny_cloud_metadata", false}, {"network.deny_kubernetes_api", false},
		{"network.profile", "unrestricted-standalone"}, {"network.profile", "none"},
		{"schema_version", 2}, {"resources.max_processes", 0}, {"platform.architectures", []string{"bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if r.Reload([][]byte{mutate(t, raw, strings.Split(tc.path, "."), tc.value, false)}, accepted) == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	for _, b := range [][]byte{append(raw, raw...), bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1), bytes.Repeat([]byte(" "), 65537), []byte(`null`)} {
		if r.Reload([][]byte{b}, accepted) == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
	if r.Reload([][]byte{raw, raw}, accepted) == nil {
		t.Fatal("duplicate profile accepted")
	}
	if r.Reload([][]byte{raw}, nil) == nil {
		t.Fatal("unresolved profile accepted")
	}
}

func TestAtomicReloadCanonicalDigestAndCopies(t *testing.T) {
	raw := fixture(t)
	r := registry(t)
	if e := r.Reload([][]byte{raw}, accepted); e != nil {
		t.Fatal(e)
	}
	p, b, d, impl, id, ok := r.Lookup("coding-medium-secure")
	if !ok {
		t.Fatal("missing profile")
	}
	p.Platform.Architectures[0] = "mutated"
	b[0] = '!'
	impl[0] = '!'
	compact := new(bytes.Buffer)
	if e := json.Compact(compact, raw); e != nil {
		t.Fatal(e)
	}
	if e := r.Reload([][]byte{compact.Bytes()}, accepted); e != nil {
		t.Fatal(e)
	}
	p, b, d2, impl, id2, _ := r.Lookup("coding-medium-secure")
	if d != d2 || id != id2 || p.Platform.Architectures[0] != "amd64" || b[0] != '{' || impl[0] != '{' {
		t.Fatal("digest or copy instability")
	}
	failed := func(runtimeprofile.Profile) ([]byte, error) { return nil, errors.New("secret provider diagnostic") }
	if e := r.Reload([][]byte{raw}, failed); e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatal("bad resolver failure")
	}
	if _, _, after, _, _, ok := r.Lookup("coding-medium-secure"); !ok || after != d {
		t.Fatal("failed reload changed active set")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				r.Lookup("coding-medium-secure")
			}
		})
	}
	for range 10 {
		if e := r.Reload([][]byte{raw}, accepted); e != nil {
			t.Fatal(e)
		}
	}
	wg.Wait()
}
