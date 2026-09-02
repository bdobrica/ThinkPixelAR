package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var testTime = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func TestWorkspaceLifecycleAndOptimisticVersion(t *testing.T) {
	w := newWorkspace(t)
	if err := w.Transition(Ready, 1, testTime.Add(time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version error = %v", err)
	}
	for _, next := range []State{Ready, Attached, Snapshotting, Attached, Degraded, Attached, Deleting, Deleted} {
		if err := w.Transition(next, w.StateVersion(), w.UpdatedAt().Add(time.Minute)); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}
	version := w.StateVersion()
	if err := w.Transition(Ready, version, w.UpdatedAt().Add(time.Minute)); !errors.Is(err, ErrIllegalTransition) || w.StateVersion() != version {
		t.Fatalf("terminal transition = %v, version %d", err, w.StateVersion())
	}
}

func TestGenerationPublicationIsMonotonicAndTenantBound(t *testing.T) {
	w := newWorkspace(t)
	g0 := newGeneration(t, 0, nil)
	if err := w.Publish(g0, w.StateVersion(), testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	number, id, ok := w.CurrentGeneration()
	if !ok || number != 0 || id != g0.ID() {
		t.Fatalf("current = %d %s %v", number, id, ok)
	}
	if err := w.Transition(Ready, w.StateVersion(), testTime.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition(Snapshotting, w.StateVersion(), testTime.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	g1 := newGeneration(t, 1, &g0)
	if err := w.Advance(g1, w.StateVersion(), testTime.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGeneration(testID(1), testID(7), testID(3), testID(2), 3, &g1, generationBinding(), testTime); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("skipped generation error = %v", err)
	}
	other := g1
	other.tenantID = testID(7)
	if err := w.Advance(other, w.StateVersion(), testTime.Add(5*time.Minute)); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("cross-tenant generation error = %v", err)
	}
}

func TestMetadataIsDefensivelyCopiedAndValidated(t *testing.T) {
	b := binding()
	w, err := New(testID(1), testID(3), b, testTime)
	if err != nil {
		t.Fatal(err)
	}
	b.Provenance[0] = '['
	if string(w.Binding().Provenance) != `{}` {
		t.Fatal("workspace retained caller-owned metadata")
	}
	bad := binding()
	bad.CapacityBytes = 0
	if _, err := New(testID(1), testID(3), bad, testTime); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("capacity error = %v", err)
	}
}

func newWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w, err := New(testID(1), testID(3), binding(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func binding() Binding {
	return Binding{SessionID: testID(2), ProviderKind: "csi", ProviderReference: "pvc:reserved", CapacityBytes: 1024, AccessMode: "single-writer", VolumeMode: "filesystem", EncryptionClass: "platform", StorageProfile: "default", ConfigDigest: digest("a"), SourceType: "empty", SourceReference: "empty:v1", Provenance: []byte(`{}`), ProvenanceDigest: digest("b"), CreateOperationID: testID(4), RetentionDisposition: "session"}
}
func generationBinding() GenerationBinding {
	return GenerationBinding{OperationID: testID(5), IntegrityAlgorithm: "sha256-tree-v1", IntegrityRoot: digest("c"), ManifestDigest: digest("d"), StorageEvidence: []byte(`{}`), StorageEvidenceDigest: digest("e"), Classification: "CONFIDENTIAL", RetentionDisposition: "session"}
}
func newGeneration(t *testing.T, number uint64, parent *Generation) Generation {
	t.Helper()
	g, err := NewGeneration(testID(1), testID(byte(6+number)), testID(3), testID(2), number, parent, generationBinding(), testTime.Add(time.Duration(number)*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func digest(char string) string { return "sha256:" + strings.Repeat(char, 64) }
func testID(last byte) primitives.ID {
	id, err := primitives.ParseID("01890f3a-5b7c-7def-8000-00000000000" + string(rune('0'+last)))
	if err != nil {
		panic(err)
	}
	return id
}
