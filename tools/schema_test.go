package tools

import (
	"encoding/json"
	"testing"
)

func TestValidateJSONSchema(t *testing.T) {
	schema, err := ParseJSONSchema(`{
		"type":"object",
		"required":["summary","confidence"],
		"properties":{
			"summary":{"type":"string"},
			"confidence":{"type":"number"},
			"files":{"type":"array","items":{"type":"string"}}
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var good any
	if err := json.Unmarshal([]byte(`{"summary":"ok","confidence":0.9,"files":["a.go"]}`), &good); err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSONSchema(good, schema); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}

	var missing any
	_ = json.Unmarshal([]byte(`{"summary":"ok"}`), &missing)
	if err := ValidateJSONSchema(missing, schema); err == nil {
		t.Fatal("expected missing-field error")
	}

	var wrongType any
	_ = json.Unmarshal([]byte(`{"summary":1,"confidence":"high"}`), &wrongType)
	if err := ValidateJSONSchema(wrongType, schema); err == nil {
		t.Fatal("expected type error")
	}
}

func TestParseJSONSchemaInvalid(t *testing.T) {
	if _, err := ParseJSONSchema("not json"); err == nil {
		t.Fatal("expected parse error")
	}
}
