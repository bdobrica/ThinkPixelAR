package openapi

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedDocumentIsOpenAPI31(t *testing.T) {
	var document struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(Document, &document); err != nil {
		t.Fatalf("embedded OpenAPI document is not JSON: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.OpenAPI)
	}
	if len(document.Paths) == 0 {
		t.Fatal("embedded OpenAPI document has no paths")
	}
}
