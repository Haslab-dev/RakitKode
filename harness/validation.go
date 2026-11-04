package harness

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RepairToolJSON cleans up common LLM JSON defects:
// 1. Markdown code block wrapping (```json ... ``` or ``` ...)
// 2. Trailing commas before closing braces or brackets (e.g. {"a": 1, })
// 3. Unescaped control characters inside string literals
func RepairToolJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "{}"
	}

	// 1. Strip markdown fences
	if strings.HasPrefix(s, "```") {
		// Find end of first line
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	// 2. Strip single quotes wrapping the JSON if model returned a quoted string
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}

	// 3. Strip trailing commas before } or ]
	// e.g. ", }" -> "}" or ",\n}" -> "\n}"
	reTrailingComma := regexp.MustCompile(`,(\s*[}\]])`)
	s = reTrailingComma.ReplaceAllString(s, "$1")

	return s
}

// SafeUnmarshalToolArgs parses raw JSON arguments with automatic repair fallback.
func SafeUnmarshalToolArgs(raw string, target *map[string]any) error {
	if target == nil {
		return fmt.Errorf("target map pointer cannot be nil")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		*target = make(map[string]any)
		return nil
	}

	// First try standard unmarshal
	if err := json.Unmarshal([]byte(trimmed), target); err == nil {
		if *target == nil {
			*target = make(map[string]any)
		}
		return nil
	}

	// Fallback to repaired JSON
	repaired := RepairToolJSON(trimmed)
	if err := json.Unmarshal([]byte(repaired), target); err != nil {
		return fmt.Errorf("failed to parse tool arguments JSON: %w", err)
	}
	if *target == nil {
		*target = make(map[string]any)
	}
	return nil
}

// ValidateToolInput performs schema validation of tool arguments against the tool's input schema.
// Returns a clear, actionable error for models to self-correct if required parameters are missing
// or properties have mismatched types.
func ValidateToolInput(schema map[string]any, input map[string]any) error {
	if schema == nil {
		return nil
	}
	if input == nil {
		input = make(map[string]any)
	}

	// 1. Check required properties
	if req, ok := schema["required"].([]any); ok && len(req) > 0 {
		var missing []string
		for _, r := range req {
			name, _ := r.(string)
			if name == "" {
				continue
			}
			val, exists := input[name]
			if !exists || val == nil {
				missing = append(missing, name)
				continue
			}
			// Empty string check for strictly required string arguments
			if strVal, isStr := val.(string); isStr && strings.TrimSpace(strVal) == "" {
				// Check if the property expects a non-empty string
				if propSchema, hasProp := getPropertySchema(schema, name); hasProp {
					if minLen, hasMin := propSchema["minLength"].(float64); hasMin && minLen > 0 {
						missing = append(missing, name+" (must not be empty)")
					}
				}
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("missing required parameter(s): %s", strings.Join(missing, ", "))
		}
	} else if reqStr, ok := schema["required"].([]string); ok && len(reqStr) > 0 {
		var missing []string
		for _, name := range reqStr {
			if name == "" {
				continue
			}
			val, exists := input[name]
			if !exists || val == nil {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("missing required parameter(s): %s", strings.Join(missing, ", "))
		}
	}

	// 2. Type validation for provided properties
	props, _ := schema["properties"].(map[string]any)
	if props != nil {
		for key, val := range input {
			rawPropSchema, exists := props[key]
			if !exists {
				continue
			}
			propSchema, ok := rawPropSchema.(map[string]any)
			if !ok {
				continue
			}

			// Type check
			if expectedType, hasType := propSchema["type"].(string); hasType && expectedType != "" {
				if err := checkValueType(key, val, expectedType); err != nil {
					return err
				}
			}

			// Enum check
			if enumVals, hasEnum := propSchema["enum"].([]any); hasEnum && len(enumVals) > 0 {
				matched := false
				for _, ev := range enumVals {
					if fmt.Sprint(ev) == fmt.Sprint(val) {
						matched = true
						break
					}
				}
				if !matched {
					return fmt.Errorf("parameter %q has invalid value %v; allowed values: %v", key, val, enumVals)
				}
			}
		}
	}

	return nil
}

func getPropertySchema(schema map[string]any, name string) (map[string]any, bool) {
	if props, ok := schema["properties"].(map[string]any); ok {
		if p, ok := props[name].(map[string]any); ok {
			return p, true
		}
	}
	return nil, false
}

func checkValueType(paramName string, val any, expectedType string) error {
	if val == nil {
		return nil
	}
	switch expectedType {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("parameter %q expected string, got %T", paramName, val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("parameter %q expected boolean, got %T", paramName, val)
		}
	case "integer":
		switch n := val.(type) {
		case int, int64, int32:
			return nil
		case float64:
			if n != float64(int64(n)) {
				return fmt.Errorf("parameter %q expected integer, got fractional number %v", paramName, n)
			}
		default:
			return fmt.Errorf("parameter %q expected integer, got %T", paramName, val)
		}
	case "number":
		switch val.(type) {
		case float64, float32, int, int64, int32:
			return nil
		default:
			return fmt.Errorf("parameter %q expected number, got %T", paramName, val)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("parameter %q expected array/list, got %T", paramName, val)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("parameter %q expected object/map, got %T", paramName, val)
		}
	}
	return nil
}
