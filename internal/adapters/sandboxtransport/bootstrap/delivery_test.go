package bootstrap

import (
	"context"
	"testing"

	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type testJournal struct {
	e                  DeliveryEntry
	planned            bool
	failSave, failBind bool
	onSave             func()
}

func (j *testJournal) SavePlan(_ context.Context, r Reference) error {
	if j.onSave != nil {
		j.onSave()
	}
	if j.failSave || j.planned {
		return ErrBootstrap
	}
	j.e.Reference = r
	j.planned = true
	return nil
}
func (j *testJournal) LoadDelivery(context.Context, primitives.ID, primitives.ID) (DeliveryEntry, error) {
	return j.e, nil
}
func (j *testJournal) BindUID(_ context.Context, r Reference) error {
	if j.failBind {
		return ErrBootstrap
	}
	j.e.Reference = r
	return nil
}
func (j *testJournal) RequestCleanup(context.Context, primitives.ID, primitives.ID) error {
	j.e.CleanupRequested = true
	return nil
}
func (j *testJournal) CompleteCleanup(context.Context, Reference) error {
	j.e.Cleaned = true
	return nil
}
func (j *testJournal) DueCleanup(context.Context, primitives.ID, int) ([]DeliveryEntry, error) {
	return []DeliveryEntry{j.e}, nil
}
func TestDeliveryPersistsBeforePublicationAndProjection(t *testing.T) {
	for _, mode := range []string{"success", "journal-failure", "uid-failure", "authority-revoked"} {
		t.Run(mode, func(t *testing.T) {
			s, c, _, r, m := fixture(t)
			j := &testJournal{}
			j.onSave = func() {
				if len(c.actions) != 0 {
					t.Fatal("external mutation before plan")
				}
			}
			calls := 0
			d, _ := NewDelivery(s, j, func(context.Context, transport.CredentialRecord) error {
				calls++
				if mode == "authority-revoked" && calls > 1 {
					return ErrBootstrap
				}
				return nil
			})
			j.failSave = mode == "journal-failure"
			j.failBind = mode == "uid-failure"
			name, err := d.Publish(context.Background(), r, m)
			if mode == "success" {
				if err != nil || name != j.e.Reference.Name || j.e.Reference.UID == "" {
					t.Fatal("projection missing durable UID", err)
				}
			} else {
				if err == nil || name != "" {
					t.Fatal("failed publication returned projection")
				}
				if mode == "journal-failure" {
					if len(c.actions) != 0 {
						t.Fatal("untracked secret created")
					}
					return
				}
				if !j.e.CleanupRequested {
					t.Fatal("cleanup not scheduled")
				}
			}
			j.failBind = false
			if err = d.Cleanup(context.Background(), r.Identity.TenantID, r.CredentialID); err != nil || !j.e.Cleaned || len(c.objects) != 0 {
				t.Fatal("exact cleanup failed", err)
			}
			if err = d.Cleanup(context.Background(), r.Identity.TenantID, r.CredentialID); err != nil {
				t.Fatal("cleanup replay", err)
			}
		})
	}
}
func TestAmbiguousAbsentPublicationRemainsTracked(t *testing.T) {
	s, _, _, r, m := fixture(t)
	ref, err := s.Plan(r, m)
	if err != nil {
		t.Fatal(err)
	}
	j := &testJournal{planned: true, e: DeliveryEntry{Reference: ref}}
	d, _ := NewDelivery(s, j, func(context.Context, transport.CredentialRecord) error { return nil })
	if err = d.Cleanup(context.Background(), r.Identity.TenantID, r.CredentialID); err != ErrAbsent || j.e.Cleaned {
		t.Fatal("absent ambiguous create forgotten")
	}
	// Model a delayed provider create after the earlier cleanup GET returned absent.
	if _, err = s.Publish(context.Background(), r, m); err != nil {
		t.Fatal(err)
	}
	if err = d.Sweep(context.Background(), r.Identity.TenantID, 1); err != nil || !j.e.Cleaned {
		t.Fatal("late create leaked", err)
	}
}
