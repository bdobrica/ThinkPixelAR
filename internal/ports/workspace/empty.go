package workspace

import "context"

// EmptyProof is a trusted provider observation, never harness-supplied evidence.
// It describes the empty boundary, not a restorable snapshot or writer grant.
type EmptyProof struct {
	WorkspaceReference, StateReference, InitializerReference, SpecDigest string
}

type EmptyInitializer interface {
	InitializeEmpty(context.Context, CreateRequest) (bool, error)
}

type EmptyReservationStore interface {
	// ReserveEmpty validates current admission against the durable Session creation
	// intent and immutable runtime resolution before reserving standalone storage.
	ReserveEmpty(context.Context, CreateRequest) (ready bool, err error)
}

func EmptyManifestDigest() string    { return digest([]byte("[]")) }
func EvidenceDigest(b []byte) string { return digest(b) }
