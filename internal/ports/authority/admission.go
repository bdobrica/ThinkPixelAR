package authority

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrDenied      = errors.New("authority: DENIED")
	ErrConflict    = errors.New("authority: CONFLICT")
	ErrUnavailable = errors.New("authority: UNAVAILABLE")
)

// Caller is supplied by trusted authentication/session authorization middleware,
// never decoded from an admission payload. Deadline is an optional trusted cutoff.
type Caller struct {
	TenantID        primitives.ID
	PrincipalDigest string
	Deadline        time.Time
}

// Request contains no caller identity, authority mode, credentials or free-form input.
// RequestDigest binds the complete canonical operation, including protected input.
type Request struct {
	SessionID                      primitives.ID
	SessionVersion, Generation     uint64
	KeyDigest, RequestDigest       string
	Profile, Network, Architecture string
	Duration                       time.Duration
	CPU, Memory, EphemeralStorage  *runtimeprofile.RequestLimit
	WorkspaceBytes, MaxProcesses   *int64
	Capabilities                   []string
}

// Grant is a transport snapshot, not a bearer credential. Consumers must persist
// and integrity-bind it to one Execution and recheck current authority and fences.
type Grant struct {
	ID                                  primitives.ID
	Mode, Issuer, PolicyDigest, Reason  string
	TenantID                            primitives.ID
	PrincipalDigest                     string
	SessionID                           primitives.ID
	SessionVersion, Generation          uint64
	RequestDigest                       string
	Runtime                             session.RuntimeBinding
	Profile                             runtimeprofile.Profile
	ProfileDigest, ImplementationDigest string
	Implementation                      []byte
	Capabilities                        []string
	IssuedAt, ExpiresAt                 time.Time
}

// Admission is the issuance portion of RunAuthority. Lifecycle validation and
// terminal reporting are separate from admission; this is not full conformance.
type Admission interface {
	Admit(context.Context, Caller, Request) (Grant, error)
}
