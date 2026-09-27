package postgres_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestCheckpointRestoreValidation(t *testing.T) {
	db, r, p, key := publicationFixture(t, true)
	content := "stored vendor conversation"
	digest := sha256.Sum256([]byte(content))
	r.VendorState[0].Size = int64(len(content))
	r.VendorState[0].Digest = hex.EncodeToString(digest[:])
	commitWorkspace(t, db, r, p)
	publisher, _ := postgres.NewCheckpoints(db, publicationAllow, publicationVerify, key)
	published, err := publisher.Publish(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	revoked, corrupt, incompatible := false, false, false
	checks := app.RestoreChecks{
		Keys: func(_ context.Context, tenant primitives.ID, issuer, id string, _ time.Time) (ed25519.PublicKey, error) {
			if revoked || tenant != r.Boundary.TenantID || issuer != "test-control-plane" || id != "test-key" {
				return nil, errors.New("private key policy detail")
			}
			return key.key.Public().(ed25519.PublicKey), nil
		},
		Compatible: func(_ context.Context, runtime app.RuntimeManifest, objects []port.VendorObject) error {
			if incompatible || runtime.AdapterBuild != r.Binding.AdapterBuildDigest || len(objects) != 1 {
				return app.ErrIncompatible
			}
			return nil
		},
		Workspace: func(_ context.Context, tenant primitives.ID, w app.WorkspaceManifest) error {
			if tenant != r.Boundary.TenantID || w.Snapshot != p.SnapshotReference || w.Root != app.Digest(p.ManifestDigest) {
				return app.ErrInvalidRestore
			}
			return nil
		},
		OpenVendor: func(_ context.Context, tenant primitives.ID, o port.VendorObject) (io.ReadCloser, error) {
			if tenant != r.Boundary.TenantID || o.Reference != r.VendorState[0].Reference {
				return nil, app.ErrInvalidRestore
			}
			if corrupt {
				return io.NopCloser(strings.NewReader("corrupt")), nil
			}
			return io.NopCloser(strings.NewReader(content)), nil
		}, MaxVendorBytes: 1024,
	}
	validator, err := app.NewRestoreValidator(checks)
	if err != nil {
		t.Fatal(err)
	}
	deny := false
	access := func(_ context.Context, tenant, session, id primitives.ID) error {
		if deny {
			return errors.New("private authorization detail")
		}
		return nil
	}
	reader, err := postgres.NewCheckpointRestores(db, access, validator)
	if err != nil {
		t.Fatal(err)
	}
	validate := func() (port.Result, error) {
		return reader.Validate(context.Background(), r.Boundary.TenantID, r.Boundary.SessionID, r.ID)
	}
	got, err := validate()
	if err != nil || !bytes.Equal(got.Manifest, published.Manifest) {
		t.Fatal("published restore", err)
	}
	for _, tc := range []struct {
		name string
		flag *bool
		want error
	}{{"revoked", &revoked, app.ErrInvalidRestore}, {"corrupt", &corrupt, app.ErrInvalidRestore}, {"incompatible", &incompatible, app.ErrIncompatible}, {"denied", &deny, workspace.ErrUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			*tc.flag = true
			defer func() { *tc.flag = false }()
			got, e := validate()
			if !errors.Is(e, tc.want) || len(got.Manifest) != 0 {
				t.Fatal(e)
			}
		})
	}
	// The validator holds the checkpoint lock through external verification, so
	// deletion cannot change its eligibility halfway through the read.
	entered, release := make(chan struct{}), make(chan struct{})
	lockedChecks := checks
	lockedChecks.Workspace = func(context.Context, primitives.ID, app.WorkspaceManifest) error {
		close(entered)
		<-release
		return nil
	}
	lockedValidator, _ := app.NewRestoreValidator(lockedChecks)
	lockedReader, _ := postgres.NewCheckpointRestores(db, access, lockedValidator)
	done := make(chan error, 1)
	go func() {
		_, e := lockedReader.Validate(context.Background(), r.Boundary.TenantID, r.Boundary.SessionID, r.ID)
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("validation did not reach storage")
	}
	lockContext, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, lockErr := db.ExecContext(lockContext, `SELECT checkpoint_id FROM checkpoints WHERE tenant_id=$1 AND checkpoint_id=$2 FOR UPDATE`, r.Boundary.TenantID, r.ID)
	cancel()
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if lockErr == nil {
		t.Fatal("checkpoint mutation lock was not excluded")
	}
	ids := concurrencyIDs(t, time.Now(), 2)
	if _, err = reader.Validate(context.Background(), ids[0], r.Boundary.SessionID, r.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatal("tenant", err)
	}
	if _, err = reader.Validate(context.Background(), r.Boundary.TenantID, ids[1], r.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatal("session", err)
	}
	// Validation is read-only, including failures; resume handles degradation and
	// durable evidence as part of its separately fenced Session operation.
	assertPublicationCount(t, db, r, 1)
	if _, err = db.Exec(`UPDATE checkpoints SET state='DELETING',state_version=state_version+1,delete_operation_id=$3,cleanup_state='PENDING',updated_at=clock_timestamp() WHERE tenant_id=$1 AND checkpoint_id=$2`, r.Boundary.TenantID, r.ID, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = validate(); !errors.Is(err, app.ErrInvalidRestore) {
		t.Fatal("deleting", err)
	}
}
