package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// CanonicalRequest includes the supplied operation identity and digest. Only
// opaque references, not credential values, belong in this durable record.
func CanonicalRequest(r AcquireRequest) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, ErrInvalid
	}
	b, err = canonical.Transform(b)
	if err != nil || len(b) > 262144 {
		return nil, ErrInvalid
	}
	return b, nil
}
func RequestDigest(r AcquireRequest) (string, error) {
	r.Operation.Digest = ""
	b, err := CanonicalRequest(r)
	if err != nil {
		return "", err
	}
	return Digest(b), nil
}
func LifecycleDigest(tenant, id primitives.ID, kind, operationID string) string {
	b, _ := json.Marshal([]string{string(tenant), string(id), kind, operationID})
	return Digest(b)
}
func Digest(b []byte) string { sum := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(sum[:]) }
