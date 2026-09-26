package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

func wsWire(t *testing.T, mutate func(*wsStorageBinding, *wsPVCBinding)) []byte {
	t.Helper()
	ro := false
	pvc := wsPVCBinding{Namespace: "agents", ClaimName: "workspace", ClaimUID: "uid-workspace", MountPath: "/workspace", ReadOnly: &ro}
	b := wsStorageBinding{Kind: "kubernetes-pvc-v1", Handle: "k8s-pvc-v1:uid-workspace"}
	if mutate != nil {
		mutate(&b, &pvc)
	}
	b.Reference, _ = json.Marshal(pvc)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWSVolumeResolver(t *testing.T) {
	cases := map[string]func(*wsStorageBinding, *wsPVCBinding){
		"valid":             nil,
		"unsupported kind":  func(b *wsStorageBinding, _ *wsPVCBinding) { b.Kind = "other" },
		"provider identity": func(b *wsStorageBinding, _ *wsPVCBinding) { b.Handle = "k8s-pvc-v1:other" },
		"foreign namespace": func(_ *wsStorageBinding, p *wsPVCBinding) { p.Namespace = "foreign" },
		"different claim":   func(_ *wsStorageBinding, p *wsPVCBinding) { p.ClaimName = "other" },
		"replacement UID":   func(b *wsStorageBinding, p *wsPVCBinding) { p.ClaimUID = "new"; b.Handle = "k8s-pvc-v1:new" },
		"wrong mount":       func(_ *wsStorageBinding, p *wsPVCBinding) { p.MountPath = "/" },
		"read only":         func(_ *wsStorageBinding, p *wsPVCBinding) { v := true; p.ReadOnly = &v },
		"missing mode":      func(_ *wsStorageBinding, p *wsPVCBinding) { p.ReadOnly = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			base, a, p, _, writes := attachmentFixture(t)
			calls := 0
			resolver, err := NewWSVolumeResolver(base, func(_ context.Context, got workspace.Attachment) ([]byte, error) {
				calls++
				if got != a {
					t.Fatal("lost reserved scope")
				}
				return wsWire(t, mutate), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				got, err := resolver.Resolve(context.Background(), a, p)
				if name == "valid" {
					if err != nil || got.WorkspaceClaim != "workspace" || got.StateClaim != "state" {
						t.Fatal(got, err)
					}
				} else if err == nil || got != (VolumeNames{}) {
					t.Fatal("accepted invalid WS binding", got, err)
				}
			}
			if calls != 2 || *writes != 0 {
				t.Fatal("cached authority or mutated storage")
			}
		})
	}
}

func TestWSVolumeResolverFailsClosed(t *testing.T) {
	for _, name := range []string{"denied", "unavailable", "malformed", "extra JSON", "live replacement", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			base, a, p, claims, writes := attachmentFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resolver, _ := NewWSVolumeResolver(base, func(context.Context, workspace.Attachment) ([]byte, error) {
				switch name {
				case "denied", "unavailable":
					return nil, errors.New("verification failed")
				case "malformed":
					return []byte(`{`), nil
				case "extra JSON":
					return append(wsWire(t, nil), []byte(` {}`)...), nil
				case "live replacement":
					claims["workspace"].UID = "replacement"
				case "cancelled":
					cancel()
				}
				return wsWire(t, nil), nil
			})
			if got, err := resolver.Resolve(ctx, a, p); err == nil || got != (VolumeNames{}) {
				t.Fatal(got, err)
			}
			if *writes != 0 {
				t.Fatal("mutated storage")
			}
		})
	}
}
