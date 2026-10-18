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

func TestPanduMicro_DefaultModelIdentityAndLocalOnlyMode(t *testing.T) {
	// 1. Constant version string must be 'pandu-micro'
	if SmallModelVersion != "pandu-micro" {
		t.Fatalf("expected SmallModelVersion to be 'pandu-micro', got %q", SmallModelVersion)
	}

	// 2. DefaultDecisionConfig must default to Jev disabled and LocalOnly mode
	cfg := DefaultDecisionConfig()
	if cfg.EnableJev {
		t.Error("expected DefaultDecisionConfig.EnableJev to be false")
	}
	if cfg.Mode != ModeLocalOnly {
		t.Errorf("expected DefaultDecisionConfig.Mode to be %q, got %q", ModeLocalOnly, cfg.Mode)
	}

	// 3. New adapter without file must report pandu-micro as ModelVersion
	adapter := NewDecisionsFileAdapter("", nil)
	status := adapter.GetStatus()
	if status.ModelVersion != "pandu-micro" {
		t.Errorf("expected status.ModelVersion 'pandu-micro', got %q", status.ModelVersion)
	}
	if status.Mode != ModeLocalOnly {
		t.Errorf("expected status.Mode %q, got %q", ModeLocalOnly, status.Mode)
	}
	if status.JevEnabled {
		t.Error("expected status.JevEnabled to be false by default")
	}
	if !status.LocalModelActive {
		t.Error("expected status.LocalModelActive to be true")
	}
}

func TestPanduMicro_ConfigToggleJevLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rakit-pandu-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "decisions.json")
	router := NewRouter("")
	adapter := NewDecisionsFileAdapter(configPath, router)

	// Step 1: Initial state is offline Pandu-Micro
	st1 := adapter.GetStatus()
	if st1.JevEnabled || st1.Mode != ModeLocalOnly {
		t.Fatalf("expected initial state Jev disabled and LocalOnly, got %+v", st1)
	}

	// Step 2: Toggle Jev on -> auto cascade mode
	err = adapter.Configure(DecisionConfig{
		EnableJev: true,
		JevAPIKey: "sk-jev-test",
	})
	if err != nil {
		t.Fatalf("failed to enable Jev: %v", err)
	}

	st2 := adapter.GetStatus()
	if !st2.JevEnabled {
		t.Error("expected Jev to be enabled after Configure(EnableJev: true)")
	}
	if st2.Mode != ModeCascade {
		t.Errorf("expected mode to auto-switch to %q, got %q", ModeCascade, st2.Mode)
	}
	if router.GetAPIKey() != "sk-jev-test" {
		t.Errorf("expected router API key to sync, got %q", router.GetAPIKey())
	}

	// Step 3: Reload from disk -> verify persisted cascade configuration
	adapterReloaded := NewDecisionsFileAdapter(configPath, router)
	stReloaded := adapterReloaded.GetStatus()
	if !stReloaded.JevEnabled || stReloaded.Mode != ModeCascade {
		t.Errorf("expected persisted cascade config upon reload, got %+v", stReloaded)
	}

	// Step 4: Toggle Jev off -> auto local-only mode
	err = adapter.Configure(DecisionConfig{
		EnableJev: false,
	})
	if err != nil {
		t.Fatalf("failed to disable Jev: %v", err)
	}

	st3 := adapter.GetStatus()
	if st3.JevEnabled {
		t.Error("expected Jev to be disabled")
	}
	if st3.Mode != ModeLocalOnly {
		t.Errorf("expected mode to auto-switch to %q, got %q", ModeLocalOnly, st3.Mode)
	}

	// Step 5: Reload from disk -> verify persisted local-only configuration
	adapterReloaded2 := NewDecisionsFileAdapter(configPath, router)
	stReloaded2 := adapterReloaded2.GetStatus()
	if stReloaded2.JevEnabled || stReloaded2.Mode != ModeLocalOnly {
		t.Errorf("expected persisted local-only config upon reload, got %+v", stReloaded2)
	}
}

func TestPanduMicro_OfflineRoutingReturnsPanduMicro(t *testing.T) {
	adapter := NewDecisionsFileAdapter("", nil)
	ctx := context.Background()
	tools := StandardBenchmarkTools()

	// Normal coding task
	routed, dec := adapter.Route(ctx, "implement unit test for neural decisions in adapter.go", tools)
	if dec == nil {
		t.Fatal("expected route decision")
	}
	if dec.Model != "pandu-micro" {
		t.Errorf("expected decision model 'pandu-micro', got %q", dec.Model)
	}
	if dec.Category != "modify" {
		t.Errorf("expected category 'modify', got %q", dec.Category)
	}
	if len(routed) == 0 {
		t.Error("expected filtered tools to be non-empty")
	}

	// Shell execution task
	routedExec, decExec := adapter.Route(ctx, "run npm install and npm test", tools)
	if decExec == nil {
		t.Fatal("expected execution route decision")
	}
	if decExec.Model != "pandu-micro" {
		t.Errorf("expected decision model 'pandu-micro', got %q", decExec.Model)
	}
	if decExec.Category != "execute" {
		t.Errorf("expected category 'execute', got %q", decExec.Category)
	}
	if len(routedExec) == 0 {
		t.Error("expected filtered execution tools")
	}
}

func TestPanduMicro_RoleAndCandidateDecisions(t *testing.T) {
	adapter := NewDecisionsFileAdapter("", nil)
	ctx := context.Background()

	// 1. Role decision for research
	roles := []string{"coding", "research", "planning"}
	roleDec := adapter.DecideAgentRole(ctx, "investigate memory leaks and search codebase symbols", roles)
	if roleDec == nil {
		t.Fatal("expected role decision")
	}
	if roleDec.Source != "pandu-micro" {
		t.Errorf("expected role decision source 'pandu-micro', got %q", roleDec.Source)
	}
	if roleDec.Role != "research" {
		t.Errorf("expected role 'research', got %q", roleDec.Role)
	}

	// 2. Candidate subagent selection
	candidates := []SubagentCandidate{
		{ID: "planner", Name: "Architect Planner", Role: "planning", Description: "Plans architectures and roadmaps"},
		{ID: "coder", Name: "Fullstack Coder", Role: "coding", Description: "Writes and refactors code"},
		{ID: "tester", Name: "QA Tester", Role: "testing", Description: "Runs and audits test suites"},
	}
	candDec := adapter.DecideSubagent(ctx, "write a unit test suite for user authentication", candidates)
	if candDec == nil {
		t.Fatal("expected candidate decision")
	}
	if candDec.SelectedID != "tester" && candDec.SelectedID != "coder" {
		t.Errorf("expected candidate 'tester' or 'coder', got %q", candDec.SelectedID)
	}
}

