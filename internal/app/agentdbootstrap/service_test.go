package agentdbootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdidentity"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type issuerStub struct{ d agentdidentity.Delivery }

func (i issuerStub) Bootstrap(context.Context, transport.Identity) (agentdidentity.Delivery, error) {
	return i.d, nil
}

type deliveryStub struct {
	cleaned bool
	fail    bool
}

func (d *deliveryStub) Publish(context.Context, transport.CredentialRecord, bootstrap.Material) (string, error) {
	if d.fail {
		return "", errors.New("restricted")
	}
	return "projection", nil
}
func (d *deliveryStub) Cleanup(ctx context.Context, _, _ primitives.ID) error {
	d.cleaned = ctx.Err() == nil
	return nil
}
func TestMaterializationFailureCleansAndDestroys(t *testing.T) {
	for _, mode := range []string{"success", "publication", "projection", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			key := []byte("test private material")
			proof := make([]byte, 32)
			proof[0] = 1
			id := transport.Identity{}
			d := &deliveryStub{fail: mode == "publication"}
			s, _ := New(issuerStub{agentdidentity.Delivery{Record: transport.CredentialRecord{Identity: id, Bootstrap: true, ExpiresAt: time.Now().Add(time.Minute)}, Certificate: transport.IssuedCertificate{PrivateKeyPEM: key}, Proof: proof}}, d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			err := s.Materialize(ctx, id, nil, nil, nil, func(ctx context.Context, name string) error {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded projection")
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "projection" {
					return errors.New("restricted")
				}
				return nil
			})
			if (err == nil) != (mode == "success") || d.cleaned != (mode != "success") || called == (mode == "publication") {
				t.Fatal("materialization order or cleanup")
			}
			for _, b := range append(key, proof...) {
				if b != 0 {
					t.Fatal("secret material retained")
				}
			}
		})
	}
}

type sweepFunc func(context.Context, primitives.ID, int) error

func (f sweepFunc) Sweep(c context.Context, t primitives.ID, n int) error { return f(c, t, n) }
func TestWorkerContinuesAcrossTenantsAndCancels(t *testing.T) {
	first, _ := primitives.NewID(time.Now())
	second, _ := primitives.NewID(time.Now())
	ids := []primitives.ID{first, second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen []primitives.ID
	w, err := NewWorker(sweepFunc(func(c context.Context, id primitives.ID, n int) error {
		if _, ok := c.Deadline(); !ok || n != 3 {
			t.Fatal("unbounded sweep")
		}
		seen = append(seen, id)
		if id == second {
			cancel()
		}
		return errors.New("restricted")
	}), ids, 5*time.Second, time.Second, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ids[0] = second
	if err = w.Run(ctx); err != nil || len(seen) != 2 || seen[0] != first || seen[1] != second {
		t.Fatal("worker stopped on failure or allowlist mutated")
	}
	if _, err = NewWorker(w.sweep, []primitives.ID{first, first}, 5*time.Second, time.Second, 3, w.logger); err == nil {
		t.Fatal("duplicate tenants")
	}
	if _, err = NewWorker(w.sweep, nil, 5*time.Second, time.Second, 3, w.logger); err == nil {
		t.Fatal("implicit tenant discovery")
	}
}

func TestWorkerRetriesAfterBudgetExpiry(t *testing.T) {
	tenant, _ := primitives.NewID(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	calls := 0
	w, err := NewWorker(sweepFunc(func(pass context.Context, _ primitives.ID, _ int) error {
		calls++
		if calls == 1 {
			<-pass.Done()
			return pass.Err()
		}
		cancel()
		return nil
	}), []primitives.ID{tenant}, 5*time.Second, 10*time.Millisecond, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Run(ctx); err != nil || calls != 2 {
		t.Fatal("worker did not retry after failed bounded pass", err, calls)
	}
}
