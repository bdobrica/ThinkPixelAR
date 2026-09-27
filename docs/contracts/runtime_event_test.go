package contracts

import (
	"bytes"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRuntimeEventSchemaInitialVersion(t *testing.T) {
	raw, err := os.ReadFile("runtime-event.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://schemas.thinkpixel.io/thinkpixelar/contracts/v1/runtime-event.json"
	if err = compiler.AddResource(uri, value); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	fixture := []byte(`{"schema_version":"thinkpixel.runtime-event/v1","event_id":"event","tenant_id":"tenant","session_id":"session","sequence":1,"aggregate_version":0,"type":"session.created","occurred_at":"2026-09-27T00:00:00Z","recorded_at":"2026-09-27T00:00:00Z","source":"agent-runtime","classification":"Internal","payload":{},"correlation":{}}`)
	for _, tc := range []struct {
		old, new string
		valid    bool
	}{
		{"", "", true}, {`"sequence":1`, `"sequence":2`, false},
		{`"session.created"`, `"session.closed"`, false},
		{`"session_id":"session"`, `"session_id":"session","execution_id":"execution"`, false},
	} {
		raw = fixture
		if tc.old != "" {
			raw = bytes.Replace(raw, []byte(tc.old), []byte(tc.new), 1)
		}
		value, err = jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if (schema.Validate(value) == nil) != tc.valid {
			t.Fatalf("unexpected validation result for %s", raw)
		}
	}
}
