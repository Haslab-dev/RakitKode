package llm

import "testing"

func TestContextWindowFor(t *testing.T) {
	tests := map[string]int{
		"anthropic/claude-sonnet-4": 200_000,
		"gpt-4o":                    128_000,
		"deepseek-chat":             128_000,
		"zhipu/glm-4.6":             200_000,
		"glm-4.5":                   128_000,
		"gemini-2.5-pro":            1_000_000,
		"unknown-model":             128_000,
	}
	for model, want := range tests {
		if got := ContextWindowFor(model); got != want {
			t.Fatalf("%s: got %d, want %d", model, got, want)
		}
	}
}
