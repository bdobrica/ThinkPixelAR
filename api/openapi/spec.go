package openapi

import _ "embed"

// Document is the validated, bundled OpenAPI 3.1 contract.
//
//go:embed openapi.json
var Document []byte
