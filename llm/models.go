package llm

import "strings"

// ContextWindowFor returns the best-known context window for a model. It is a
// conservative table used for context budgeting and the TUI status line;
// unknown models default to 128k.
func ContextWindowFor(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gemini"):
		return 1_000_000
	case strings.Contains(m, "claude"):
		return 200_000
	case strings.Contains(m, "glm-4.6"):
		return 200_000
	case strings.Contains(m, "glm"):
		return 128_000
	case strings.Contains(m, "gpt-4.1"), strings.Contains(m, "gpt-4o"):
		return 128_000
	case strings.Contains(m, "deepseek"):
		return 128_000
	case strings.Contains(m, "qwen"):
		return 128_000
	case strings.Contains(m, "grok"):
		return 131_072
	}
	return 128_000
}
