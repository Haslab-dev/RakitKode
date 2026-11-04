package harness

import (
	"testing"
)

func TestRepairToolJSON(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean json",
			input:    `{"path": "main.go"}`,
			expected: `{"path": "main.go"}`,
		},
		{
			name:     "markdown fence wrapped",
			input:    "```json\n{\"path\": \"main.go\"}\n```",
			expected: `{"path": "main.go"}`,
		},
		{
			name:     "trailing commas",
			input:    `{"a": 1, "b": [1, 2, ], }`,
			expected: `{"a": 1, "b": [1, 2 ] }`,
		},
		{
			name:     "single quote wrapped",
			input:    `'{"path": "test.txt"}'`,
			expected: `{"path": "test.txt"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repaired := RepairToolJSON(tc.input)
			if repaired != tc.expected {
				t.Errorf("got %q, want %q", repaired, tc.expected)
			}
		})
	}
}

func TestSafeUnmarshalToolArgs(t *testing.T) {
	var target map[string]any
	raw := "```json\n{\"command\": \"go test ./...\", }\n```"
	if err := SafeUnmarshalToolArgs(raw, &target); err != nil {
		t.Fatalf("SafeUnmarshalToolArgs failed: %v", err)
	}
	if target["command"] != "go test ./..." {
		t.Errorf("expected command 'go test ./...', got %v", target["command"])
	}
}

func TestValidateToolInput(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"path", "content"},
		"properties": map[string]any{
			"path": map[string]any{
				"type": "string",
			},
			"content": map[string]any{
				"type": "string",
			},
			"overwrite": map[string]any{
				"type": "boolean",
			},
			"mode": map[string]any{
				"type": "string",
				"enum": []any{"read", "write", "append"},
			},
		},
	}

	// Missing required property
	err := ValidateToolInput(schema, map[string]any{"path": "file.txt"})
	if err == nil {
		t.Fatal("expected error for missing 'content'")
	}

	// Type mismatch
	err = ValidateToolInput(schema, map[string]any{
		"path":      "file.txt",
		"content":   "hello",
		"overwrite": "not-a-bool",
	})
	if err == nil {
		t.Fatal("expected error for non-boolean overwrite")
	}

	// Invalid enum
	err = ValidateToolInput(schema, map[string]any{
		"path":    "file.txt",
		"content": "hello",
		"mode":    "invalid_mode",
	})
	if err == nil {
		t.Fatal("expected error for invalid enum value")
	}

	// Valid input
	err = ValidateToolInput(schema, map[string]any{
		"path":      "file.txt",
		"content":   "hello",
		"overwrite": true,
		"mode":      "write",
	})
	if err != nil {
		t.Fatalf("expected valid input to pass, got: %v", err)
	}
}
