package authority

import "regexp"

const LocalMode = "local"
const LocalIssuer = "thinkpixelar/local"

// Identity is safe display metadata from a trusted adapter or persisted grant.
// It is not proof of admission, validity, or AG availability.
type Identity struct {
	Mode   string `json:"authority_mode"`
	Issuer string `json:"authority_issuer"`
}

// IdentityProvider is supplied by service composition, never by an API caller.
type IdentityProvider interface {
	Identity() Identity
}

var safeIssuer = regexp.MustCompile(`^[a-z][a-z0-9./_-]{0,127}$`)

func (i Identity) Valid() bool {
	switch i.Mode {
	case LocalMode:
		return i.Issuer == LocalIssuer
	case "thinkpixelag":
		return safeIssuer.MatchString(i.Issuer) && i.Issuer != LocalIssuer
	default:
		return false
	}
}
