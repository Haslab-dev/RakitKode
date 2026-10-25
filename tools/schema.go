package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseJSONSchema parses an output_schema string into a schema object.
func ParseJSONSchema(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty schema")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, err
	}
	return schema, nil
}

// ValidateJSONSchema performs a pragmatic subset of JSON Schema validation:
// type, required, properties (type-checked) and arrays/items. It is enough to
// catch malformed sub-agent structured output without pulling in a full
// validator.
func ValidateJSONSchema(value any, schema map[string]any) error {
	if schema == nil {
		return nil
	}
	if t, ok := schema["type"].(string); ok && t != "" {
		if err := validateType(value, t); err != nil {
			return err
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if name == "" {
					continue
				}
				if _, exists := v[name]; !exists {
					return fmt.Errorf("missing required field %q", name)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, rawProp := range props {
				prop, ok := rawProp.(map[string]any)
				if !ok {
					continue
				}
				child, exists := v[name]
				if !exists {
					continue
				}
				if err := ValidateJSONSchema(child, prop); err != nil {
					return fmt.Errorf("field %q: %w", name, err)
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range v {
				if err := ValidateJSONSchema(item, items); err != nil {
					return fmt.Errorf("item %d: %w", i, err)
				}
			}
		}
	}
	return nil
}

func validateType(value any, want string) error {
	ok := false
	switch want {
	case "object":
		_, ok = value.(map[string]any)
	case "array":
		_, ok = value.([]any)
	case "string":
		_, ok = value.(string)
	case "boolean":
		_, ok = value.(bool)
	case "number":
		switch value.(type) {
		case float64, float32, int, int64:
			ok = true
		}
	case "integer":
		switch n := value.(type) {
		case float64:
			ok = n == float64(int64(n))
		case int, int64:
			ok = true
		}
	case "null":
		ok = value == nil
	default:
		return nil // unknown keyword: don't reject
	}
	if !ok {
		return fmt.Errorf("expected %s, got %T", want, value)
	}
	return nil
}
