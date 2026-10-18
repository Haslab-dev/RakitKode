package decisions

import (
	"context"
	"os"
	"testing"
)

const liveJevAPIKey = DefaultLiveJevAPIKey

func getLiveJevKey() string {
	if k := os.Getenv("TYPESAFE_API_KEY"); k != "" {
		return k
	}
	return liveJevAPIKey
}

func BenchmarkSmallNeuralEngine_ExtractFeatures(b *testing.B) {
	engine := NewSmallNeuralEngine()
	prompt := "search all files matching *.go and inspect the implementation"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.ExtractFeatures(prompt)
	}
}

func BenchmarkSmallNeuralEngine_Forward(b *testing.B) {
	engine := NewSmallNeuralEngine()
	x := engine.ExtractFeatures("search all files matching *.go and inspect the implementation")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = engine.Forward(x)
	}
}

func BenchmarkSmallNeuralEngine_PredictCategory(b *testing.B) {
	engine := NewSmallNeuralEngine()
	prompt := "edit main.go to update version string to v0.2.8"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = engine.PredictCategory(prompt)
	}
}

func BenchmarkSmallNeuralEngine_PredictRole(b *testing.B) {
	engine := NewSmallNeuralEngine()
	task := "investigate codebase and trace potential security leaks in authentication"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = engine.PredictRole(task)
	}
}

func BenchmarkLocalDecisionEngine_Route(b *testing.B) {
	lde := NewLocalDecisionEngine()
	tools := sampleTools()
	prompt := "edit the main function to add benchmark flag"
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = lde.Route(ctx, prompt, tools)
	}
}

func BenchmarkJevLive_Route(b *testing.B) {
	apiKey := getLiveJevKey()
	if apiKey == "" {
		b.Skip("skipping live Jev benchmark: no TYPESAFE_API_KEY configured")
	}
	router := NewRouter(apiKey)
	tools := sampleTools()
	prompt := "edit the main function to add benchmark flag"
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = router.Route(ctx, prompt, tools)
	}
}

// TestBenchmarkComparison_LocalVsJevLive executes a head-to-head empirical benchmark
// comparing the Local Decision Small Neural Model against the LIVE TypeSafe Jev API.
func TestBenchmarkComparison_LocalVsJevLive(t *testing.T) {
	apiKey := getLiveJevKey()
	if apiKey == "" {
		t.Skip("skipping live Jev benchmark: no TYPESAFE_API_KEY configured")
	}

	report, err := RunBenchmarkSuite(apiKey, 2000, 12)
	if err != nil {
		t.Fatalf("failed to run benchmark suite: %v", err)
	}

	t.Log(report.String())

	// Verifications
	if report.SpeedupMean < 100.0 {
		t.Fatalf("expected local model to be at least 100x faster than live Jev network call, got %.1fx", report.SpeedupMean)
	}
	if report.AgreementRate < 80.0 {
		t.Fatalf("expected category agreement >= 80%%, got %.1f%%", report.AgreementRate)
	}
}
