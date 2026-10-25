package llm

import (
	"fmt"
	"strings"
)

// ModelPricing defines per-million token rates in USD.
type ModelPricing struct {
	PromptPerMillion     float64
	CompletionPerMillion float64
	CacheReadPerMillion  float64
}

// Default pricing table for standard models (rates per 1M tokens).
var pricingTable = map[string]ModelPricing{
	// Anthropic
	"claude-3-7-sonnet": {PromptPerMillion: 3.00, CompletionPerMillion: 15.00, CacheReadPerMillion: 0.30},
	"claude-3-5-sonnet": {PromptPerMillion: 3.00, CompletionPerMillion: 15.00, CacheReadPerMillion: 0.30},
	"claude-3-5-haiku":  {PromptPerMillion: 0.80, CompletionPerMillion: 4.00, CacheReadPerMillion: 0.08},
	"claude-3-opus":     {PromptPerMillion: 15.00, CompletionPerMillion: 75.00, CacheReadPerMillion: 1.50},

	// OpenAI
	"gpt-4o":      {PromptPerMillion: 2.50, CompletionPerMillion: 10.00, CacheReadPerMillion: 1.25},
	"gpt-4o-mini": {PromptPerMillion: 0.15, CompletionPerMillion: 0.60, CacheReadPerMillion: 0.075},
	"o1":          {PromptPerMillion: 15.00, CompletionPerMillion: 60.00, CacheReadPerMillion: 7.50},
	"o3-mini":     {PromptPerMillion: 1.10, CompletionPerMillion: 4.40, CacheReadPerMillion: 0.55},

	// DeepSeek
	"deepseek-chat":     {PromptPerMillion: 0.14, CompletionPerMillion: 0.28, CacheReadPerMillion: 0.014},
	"deepseek-coder":    {PromptPerMillion: 0.14, CompletionPerMillion: 0.28, CacheReadPerMillion: 0.014},
	"deepseek-reasoner": {PromptPerMillion: 0.55, CompletionPerMillion: 2.19, CacheReadPerMillion: 0.14},

	// Google Gemini
	"gemini-2.0-flash": {PromptPerMillion: 0.10, CompletionPerMillion: 0.40, CacheReadPerMillion: 0.025},
	"gemini-1.5-flash": {PromptPerMillion: 0.075, CompletionPerMillion: 0.30, CacheReadPerMillion: 0.01875},
	"gemini-1.5-pro":   {PromptPerMillion: 1.25, CompletionPerMillion: 5.00, CacheReadPerMillion: 0.3125},

	// Local / Ollama (free)
	"ollama": {PromptPerMillion: 0.0, CompletionPerMillion: 0.0, CacheReadPerMillion: 0.0},
	"local":  {PromptPerMillion: 0.0, CompletionPerMillion: 0.0, CacheReadPerMillion: 0.0},
}

// FindPricing returns the pricing rule matching the given model name or ID.
func FindPricing(model string) ModelPricing {
	lower := strings.ToLower(model)
	// Strip provider prefix if present (e.g. "anthropic/claude-3-5-sonnet")
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		lower = lower[idx+1:]
	}

	for k, p := range pricingTable {
		if strings.Contains(lower, k) {
			return p
		}
	}

	// Default fallback: modest blended rate ($1.00 / $3.00)
	if strings.Contains(lower, "haiku") || strings.Contains(lower, "mini") || strings.Contains(lower, "flash") {
		return ModelPricing{PromptPerMillion: 0.20, CompletionPerMillion: 0.80, CacheReadPerMillion: 0.05}
	}
	if strings.Contains(lower, "qwen") || strings.Contains(lower, "llama") || strings.Contains(lower, "mistral") || strings.Contains(lower, "local") {
		return ModelPricing{PromptPerMillion: 0.0, CompletionPerMillion: 0.0, CacheReadPerMillion: 0.0}
	}

	return ModelPricing{PromptPerMillion: 1.50, CompletionPerMillion: 6.00, CacheReadPerMillion: 0.375}
}

// EstimateCost calculates the estimated cost in USD for the given token usage.
func EstimateCost(model string, promptTokens, completionTokens int, cacheReadTokens ...int) float64 {
	p := FindPricing(model)
	var cacheTokens int
	if len(cacheReadTokens) > 0 {
		cacheTokens = cacheReadTokens[0]
	}

	effectivePrompt := promptTokens - cacheTokens
	if effectivePrompt < 0 {
		effectivePrompt = 0
	}

	cost := (float64(effectivePrompt) / 1_000_000.0) * p.PromptPerMillion
	cost += (float64(completionTokens) / 1_000_000.0) * p.CompletionPerMillion
	cost += (float64(cacheTokens) / 1_000_000.0) * p.CacheReadPerMillion
	return cost
}

// FormatCost formats a USD cost into a human-readable display string.
func FormatCost(cost float64) string {
	if cost <= 0.000001 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}
