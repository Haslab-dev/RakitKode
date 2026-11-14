package llm

import (
	"math"
	"testing"
)

func TestEstimateCostAndSavings(t *testing.T) {
	// DeepSeek chat: Prompt: 0.14, Output: 0.28, CacheRead: 0.014 (1/10 of prompt)
	model := "deepseek-chat"
	promptTokens := 100_000
	completionTokens := 10_000
	cachedTokens := 90_000 // 90% cache hit

	// Without cache tokens passed
	gross := EstimateCost(model, promptTokens, completionTokens)
	expectedGross := (100_000.0 / 1_000_000.0 * 0.14) + (10_000.0 / 1_000_000.0 * 0.28)
	if math.Abs(gross-expectedGross) > 1e-6 {
		t.Fatalf("gross cost = %v, want %v", gross, expectedGross)
	}

	// With cache tokens passed
	net := EstimateCost(model, promptTokens, completionTokens, cachedTokens)
	expectedNet := (10_000.0 / 1_000_000.0 * 0.14) + (10_000.0 / 1_000_000.0 * 0.28) + (90_000.0 / 1_000_000.0 * 0.014)
	if math.Abs(net-expectedNet) > 1e-6 {
		t.Fatalf("net cost = %v, want %v", net, expectedNet)
	}

	savings, pct := EstimateSavings(model, promptTokens, completionTokens, cachedTokens)
	expectedSavings := gross - net
	if math.Abs(savings-expectedSavings) > 1e-6 {
		t.Fatalf("savings = %v, want %v", savings, expectedSavings)
	}
	expectedPct := (expectedSavings / gross) * 100.0
	if math.Abs(pct-expectedPct) > 1e-3 {
		t.Fatalf("savings pct = %v, want %v", pct, expectedPct)
	}

	// Zero cache tokens
	s0, p0 := EstimateSavings(model, promptTokens, completionTokens, 0)
	if s0 != 0 || p0 != 0 {
		t.Fatalf("expected 0 savings for 0 cached tokens, got %v, %v", s0, p0)
	}
}
