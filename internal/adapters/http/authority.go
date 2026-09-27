package http

import (
	"encoding/json"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
)

// authorityDiagnostic snapshots the adapter wired by trusted service composition.
// Deployment identity must never stand in for an Execution's persisted binding.
func authorityDiagnostic(provider authority.IdentityProvider) (authority.Identity, string, error) {
	id := authority.Identity{Mode: "unconfigured"}
	message := "No execution authority adapter configured."
	if provider != nil {
		id = provider.Identity()
		if !id.Valid() {
			return authority.Identity{}, "", errors.New("invalid authority diagnostic identity")
		}
		message = "ThinkPixelAG authority adapter configured; this is not an availability or admission check."
		if id.Mode == authority.LocalMode {
			message = "Standalone local authority; no ThinkPixelAG governance."
		}
	}
	body, err := json.Marshal(struct {
		authority.Identity
		Description string `json:"description"`
	}{id, message})
	return id, string(body), err
}
