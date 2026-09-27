// Package contracts embeds normative schemas directly from their source files.
package contracts

import _ "embed"

//go:embed runtime-profile.schema.json
var runtimeProfileSchema string

func RuntimeProfileSchema() string { return runtimeProfileSchema }

//go:embed agent-runtime-spec.schema.json
var agentRuntimeSpecSchema string

func AgentRuntimeSpecSchema() string { return agentRuntimeSpecSchema }

//go:embed checkpoint-manifest.schema.json
var checkpointManifestSchema string

func CheckpointManifestSchema() string { return checkpointManifestSchema }
