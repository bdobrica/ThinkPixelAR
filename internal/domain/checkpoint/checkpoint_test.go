package checkpoint

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestCheckpointCommitBindsImmutableIntegrity(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	c, err := New(id(1), id(2), validBindingValue(), now)
	if err != nil {
		t.Fatal(err)
	}
	i := validIntegrityValue(now)
	if err := c.Commit(i, 0, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if c.State() != Committed || c.StateVersion() != 1 {
		t.Fatalf("state/version = %s/%d", c.State(), c.StateVersion())
	}
	i.CanonicalManifest[0] = '['
	i.Exclusions[0] = "changed"
	got, ok := c.Integrity()
	if !ok || got.CanonicalManifest[0] != '{' || got.Exclusions[0] != RequiredExclusions[0] {
		t.Fatal("checkpoint did not own integrity data")
	}
	got.VendorState[0] = '{'
	again, _ := c.Integrity()
	if again.VendorState[0] != '[' {
		t.Fatal("integrity getter leaked mutable data")
	}
}

func TestCheckpointLifecycleAndOptimisticVersion(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	c, _ := New(id(1), id(2), validBindingValue(), now)
	if err := c.Commit(validIntegrityValue(now), 1, now.Add(time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version error = %v", err)
	}
	if err := c.Commit(validIntegrityValue(now), 0, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(1, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkDeleted(2, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.DeletedAt(); !ok || c.State() != Deleted {
		t.Fatal("checkpoint was not deleted")
	}
	if err := c.Delete(3, now.Add(4*time.Minute)); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("terminal delete error = %v", err)
	}
}

func TestCheckpointRejectsInvalidGenerationIntegrityAndExclusions(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	b := validBindingValue()
	b.WorkspaceGenerationID = ""
	if _, err := New(id(1), id(2), b, now); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("binding error = %v", err)
	}
	c, _ := New(id(1), id(2), validBindingValue(), now)
	i := validIntegrityValue(now)
	i.Exclusions = i.Exclusions[:6]
	if err := c.Commit(i, 0, now.Add(time.Minute)); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("exclusion error = %v", err)
	}
	i = validIntegrityValue(now)
	i.PayloadDigest = "short"
	if err := c.Commit(i, 0, now.Add(time.Minute)); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("digest error = %v", err)
	}
}

func validBindingValue() Binding {
	return Binding{
		SessionID: id(3), WorkspaceID: id(4), WorkspaceGenerationID: id(5), WorkspaceGeneration: 1,
		OperationID: id(6), Purpose: PurposeSuspend, RuntimeSpecID: "runtime-1", RuntimeSpecDigest: digest('a'),
		AdapterKind: "codex", AdapterVersion: "1", AdapterBuildDigest: digest('b'), ProtocolName: "app-server", ProtocolVersion: "1",
		StateFormatName: "thread", StateFormatVersion: "1", RuntimeProfileDigest: digest('c'), RetentionDisposition: "session",
	}
}
func validIntegrityValue(now time.Time) Integrity {
	return Integrity{
		CanonicalManifest: []byte(`{"schema_version":"thinkpixel.checkpoint/v1"}`), Canonicalization: "RFC8785-JCS", DigestAlgorithm: "sha-256",
		PayloadDigest: digest('d'), CompositeRoot: digest('e'), SignatureAlgorithm: "EdDSA", Signer: "checkpoint-signer", KeyID: "key-1",
		Signature: string(makeBytes('s', 64)), SignedAt: now, VendorState: []byte(`[]`), Exclusions: append([]string(nil), RequiredExclusions...),
	}
}
func id(n int) primitives.ID {
	return primitives.ID(fmt.Sprintf("01890f3a-5b7c-7def-8000-%012d", n))
}
func digest(ch byte) string { return string(makeBytes(ch, 64)) }
func makeBytes(ch byte, n int) []byte {
	value := make([]byte, n)
	for i := range value {
		value[i] = ch
	}
	return value
}
