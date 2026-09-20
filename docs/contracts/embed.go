// Package contracts embeds normative schemas directly from their source files.
package contracts

import _ "embed"

//go:embed runtime-profile.schema.json
var runtimeProfileSchema string

func RuntimeProfileSchema() string { return runtimeProfileSchema }
