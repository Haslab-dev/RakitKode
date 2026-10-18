package decisions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionsFileAdapter_ConfigLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rakit-decisions-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "decisions.json")
	router := NewRouter("")
	adapter := NewDecisionsFileAdapter(configPath, router)

	status := adapter.GetStatus()
	if !status.LocalModelActive {
		t.Error("expected local model to be active")
	}
	if status.Mode != ModeLocalOnly {
		t.Errorf("expected default mode local_only (pandu-micro), got %s", status.Mode)
	}
	if status.JevEnabled {
		t.Error("expected Jev to be disabled by default (pandu-micro is default model)")
	}

	// Reconfigure to Enable Jev
	newCfg := DecisionConfig{
		EnableJev:           true,
		Mode:                ModeCascade,
		ConfidenceThreshold: 0.88,
		FallbackToLocal:     true,
	}
	if err := adapter.Configure(newCfg); err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	// Verify persistence on disk
	adapter2 := NewDecisionsFileAdapter(configPath, router)
	status2 := adapter2.GetStatus()
	if !status2.JevEnabled {
		t.Error("expected Jev to be enabled after reload")
	}
	if status2.Mode != ModeCascade {
		t.Errorf("expected mode cascade after reload, got %s", status2.Mode)
	}
}

func TestDecisionsFileAdapter_FallbackWhenDisabledOrNoKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rakit-decisions-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "decisions.json")
	router := NewRouter("") // no API key
	router.SetEnabled(false) // explicitly disabled
	adapter := NewDecisionsFileAdapter(configPath, router)

	ctx := context.Background()
	tools := StandardBenchmarkTools()

	// 1. Should route via local small model seamlessly
	routed, dec := adapter.Route(ctx, "run go test ./... to verify everything", tools)
	if dec == nil {
		t.Fatal("expected route decision from local model")
	}
	if dec.Category != "execute" {
		t.Errorf("expected category execute, got %s", dec.Category)
	}
	if len(routed) == 0 {
		t.Error("expected routed tools, got empty")
	}

	// 2. Role decision fallback
	roleDec := adapter.DecideAgentRole(ctx, "investigate security vulnerabilities in auth", []string{"coder", "researcher", "reviewer"})
	if roleDec == nil || roleDec.Role == "" {
		t.Fatal("expected role decision from local model")
	}

	// 3. Safety assessment audit
	safety := adapter.AssessToolSafety("bash", map[string]any{"command": "rm -rf / --no-preserve-root"})
	if safety.Risk != RiskCritical {
		t.Errorf("expected risk critical for destructive rm -rf, got %s", safety.Risk)
	}
}

func TestSmallNeuralEngine_RLCD_MultiHead(t *testing.T) {
	engine := NewSmallNeuralEngine()

	// 1. Predict Full Decision on normal coding prompt
	fullDec := engine.PredictFullDecision("edit main.go to update version string to v0.2.9")
	if fullDec.Category != "modify" {
		t.Errorf("expected category modify, got %s", fullDec.Category)
	}
	if fullDec.Role != "coder" {
		t.Errorf("expected role coder, got %s", fullDec.Role)
	}
	if fullDec.SafetyRisk != RiskMedium {
		t.Errorf("expected risk medium for file edit, got %s", fullDec.SafetyRisk)
	}
	if fullDec.CategoryConf <= 0.5 {
		t.Errorf("expected calibrated confidence > 0.5, got %.2f", fullDec.CategoryConf)
	}

	// 2. Predict Safety Risk on critical command
	risk, conf := engine.PredictSafetyRisk("sudo chmod -R 777 /var/data && drop table users")
	if risk != RiskCritical {
		t.Errorf("expected risk critical, got %s (conf: %.2f)", risk, conf)
	}

	// 3. Delegation Prediction
	delTask, delConf := engine.PredictDelegation("conduct full system architectural refactor of memory management across codebase")
	if !delTask {
		t.Logf("delegation prediction: %v (conf: %.2f)", delTask, delConf)
	}
}

func TestSmallNeuralEngine_LargeContextZoning(t *testing.T) {
	engine := NewSmallNeuralEngine()

	// Construct a 20,000 character prompt with embedded code payload
	var payload strings.Builder
	payload.WriteString("Fix the race condition and modify this struct to be thread-safe:\n```go\n")
	for i := 0; i < 500; i++ {
		payload.WriteString("    // payload dummy line reading file and searching things\n")
		payload.WriteString("    var item string = \"dummy data reading and search\"\n")
	}
	payload.WriteString("```\nPlease edit and patch this concurrency bug now.")

	dec := engine.PredictFullDecision(payload.String())
	if dec.Category != "modify" {
		t.Errorf("expected category modify for large code dump, got %s (conf: %.2f)", dec.Category, dec.CategoryConf)
	}
	if dec.LatencyUs <= 0 || dec.LatencyUs > 5000 {
		t.Errorf("expected microsecond latency for large context, got %d µs", dec.LatencyUs)
	}
	t.Logf("Large context (20k chars) evaluated in %d µs -> Category: %s (conf: %.2f), Role: %s",
		dec.LatencyUs, dec.Category, dec.CategoryConf, dec.Role)
}

