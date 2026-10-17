package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Haslab-dev/RakitKode/llm"
)

// DecisionMode specifies how routing decisions are negotiated between local and cloud models.
type DecisionMode string

const (
	// ModeCascade uses Tier-0 Local Small Model for high-confidence decisions (<40 µs),
	// escalating to Tier-1 TypeSafe Jev (RLCD) when confidence is ambiguous,
	// and falling back to Local if Jev is disabled, has no key, or errors.
	ModeCascade DecisionMode = "cascade"

	// ModeJevOnly targets TypeSafe Jev cloud RLCD model directly,
	// falling back to local small model only on network failure or missing key.
	ModeJevOnly DecisionMode = "jev_only"

	// ModeLocalOnly uses 100% offline, in-process Local Small Neural Model (<40 µs, zero network).
	ModeLocalOnly DecisionMode = "local_only"
)

// DecisionConfig stores persisted configuration for the decision engine.
type DecisionConfig struct {
	EnableJev           bool         `json:"enable_jev"`
	JevAPIKey           string       `json:"jev_api_key,omitempty"`
	JevEndpoint         string       `json:"jev_endpoint,omitempty"`
	Mode                DecisionMode `json:"mode"`
	ConfidenceThreshold float64      `json:"confidence_threshold"`
	FallbackToLocal     bool         `json:"fallback_to_local"`
}

// DefaultDecisionConfig returns the production configuration defaults.
func DefaultDecisionConfig() DecisionConfig {
	return DecisionConfig{
		EnableJev:           true,
		Mode:                ModeCascade,
		ConfidenceThreshold: 0.90,
		FallbackToLocal:     true,
	}
}

// DecisionStatus reports runtime status of the decision engine.
type DecisionStatus struct {
	JevEnabled       bool         `json:"jev_enabled"`
	JevActive        bool         `json:"jev_active"`
	HasKey           bool         `json:"has_key"`
	LocalModelActive bool         `json:"local_model_active"`
	ModelVersion     string       `json:"model_version"`
	Mode             DecisionMode `json:"mode"`
	Endpoint         string       `json:"endpoint"`
}

// DecisionAdapter provides a unified contract for agent routing and decisions.
type DecisionAdapter interface {
	Route(ctx context.Context, prompt string, allTools []llm.ToolDefinition) ([]llm.ToolDefinition, *RouteDecision)
	DecideAgentRole(ctx context.Context, task string, availableRoles []string) *RoleDecision
	DecideSubagent(ctx context.Context, task string, candidates []SubagentCandidate) *SubagentDecision
	AssessToolSafety(toolName string, args map[string]any) SafetyAssessment
	SuggestReflection(toolName string, args map[string]any, output string) string
	GetStatus() DecisionStatus
	GetStats() map[string]any
	Configure(cfg DecisionConfig) error
	Save() error
}

// DecisionsFileAdapter manages persistent configuration and execution of Jev
// and the Local Small Neural Model behind a single file-backed adapter.
type DecisionsFileAdapter struct {
	mu          sync.RWMutex
	filePath    string
	config      DecisionConfig
	router      *Router
	localEngine *LocalDecisionEngine
}

// NewDecisionsFileAdapter instantiates a file-backed decision adapter.
// If filePath is empty, defaults to ~/.rakit/decisions.json.
func NewDecisionsFileAdapter(filePath string, router *Router) *DecisionsFileAdapter {
	if filePath == "" {
		home, _ := os.UserHomeDir()
		filePath = filepath.Join(home, ".rakit", "decisions.json")
	}

	cfg := DefaultDecisionConfig()
	if router != nil {
		cfg.EnableJev = router.GetEnabled()
		cfg.JevAPIKey = router.GetAPIKey()
	}

	adapter := &DecisionsFileAdapter{
		filePath:    filePath,
		config:      cfg,
		router:      router,
		localEngine: NewLocalDecisionEngine(),
	}

	_ = adapter.Load()
	return adapter
}

// Router returns the underlying remote Jev router.
func (a *DecisionsFileAdapter) Router() *Router {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.router
}

// LocalEngine returns the underlying local decision engine.
func (a *DecisionsFileAdapter) LocalEngine() *LocalDecisionEngine {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.localEngine
}

// Load reads the decisions configuration file from disk.
func (a *DecisionsFileAdapter) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read decisions config: %w", err)
	}

	var cfg DecisionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse decisions config: %w", err)
	}

	a.config = cfg
	if a.router != nil {
		a.router.SetEnabled(cfg.EnableJev)
		if cfg.JevAPIKey != "" {
			a.router.SetAPIKey(cfg.JevAPIKey)
		}
		if cfg.JevEndpoint != "" {
			a.router.SetEndpoint(cfg.JevEndpoint)
		}
	}

	return nil
}

// Save writes current decisions configuration to disk.
func (a *DecisionsFileAdapter) Save() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	dir := filepath.Dir(a.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal decisions config: %w", err)
	}

	return os.WriteFile(a.filePath, data, 0o644)
}

// Configure updates settings and synchronizes the router.
func (a *DecisionsFileAdapter) Configure(cfg DecisionConfig) error {
	a.mu.Lock()
	a.config = cfg
	if a.router != nil {
		a.router.SetEnabled(cfg.EnableJev)
		if cfg.JevAPIKey != "" {
			a.router.SetAPIKey(cfg.JevAPIKey)
		}
		if cfg.JevEndpoint != "" {
			a.router.SetEndpoint(cfg.JevEndpoint)
		}
	}
	a.mu.Unlock()

	return a.Save()
}

// GetStatus returns the current operational status across local and remote models.
func (a *DecisionsFileAdapter) GetStatus() DecisionStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()

	hasKey := false
	jevActive := false
	endpoint := defaultEndpoint
	if a.router != nil {
		hasKey = a.router.GetAPIKey() != ""
		jevActive = a.router.IsEnabled()
	}

	return DecisionStatus{
		JevEnabled:       a.config.EnableJev,
		JevActive:        jevActive,
		HasKey:           hasKey,
		LocalModelActive: true,
		ModelVersion:     SmallModelVersion,
		Mode:             a.config.Mode,
		Endpoint:         endpoint,
	}
}

// Route directs tool pruning according to configured mode (Cascade, JevOnly, LocalOnly)
// with guaranteed fallback to the in-process local neural model.
func (a *DecisionsFileAdapter) Route(ctx context.Context, prompt string, allTools []llm.ToolDefinition) ([]llm.ToolDefinition, *RouteDecision) {
	a.mu.RLock()
	cfg := a.config
	router := a.router
	local := a.localEngine
	a.mu.RUnlock()

	if strings.TrimSpace(prompt) == "" || len(allTools) == 0 {
		return allTools, nil
	}

	// 1. Local-Only Mode or Jev Disabled or No Key -> Run Local Small Model directly (<40 µs)
	if cfg.Mode == ModeLocalOnly || !cfg.EnableJev || router == nil || !router.IsEnabled() {
		return local.Route(ctx, prompt, allTools)
	}

	// 2. Cascade Mode: Tier-0 Local Check
	if cfg.Mode == ModeCascade {
		neural := local.NeuralEngine()
		cat, conf, needsTools := neural.PredictCategory(prompt)

		// High confidence local prediction: route immediately without cloud latency
		threshold := cfg.ConfidenceThreshold
		if threshold <= 0 {
			threshold = 0.90
		}
		if conf >= threshold && needsTools > 0.8 {
			filtered := filterToolsByCategory(cat, prompt, allTools)
			return filtered, &RouteDecision{
				Category:   cat,
				Confidence: conf,
				NeedsTools: needsTools,
				Model:      SmallModelVersion,
			}
		}
	}

	// 3. Cloud Jev Routing (with automatic local fallback on error)
	routed, dec := router.Route(ctx, prompt, allTools)
	if dec != nil && (dec.Model == "jev" || strings.HasPrefix(dec.Model, "jev-")) {
		return routed, dec
	}

	// Fallback triggered inside router
	return routed, dec
}

// DecideAgentRole resolves sub-agent role recommendations.
func (a *DecisionsFileAdapter) DecideAgentRole(ctx context.Context, task string, availableRoles []string) *RoleDecision {
	a.mu.RLock()
	cfg := a.config
	router := a.router
	local := a.localEngine
	a.mu.RUnlock()

	if cfg.Mode == ModeLocalOnly || !cfg.EnableJev || router == nil || !router.IsEnabled() {
		return local.DecideRole(ctx, task, availableRoles)
	}

	dec := router.DecideAgentRole(ctx, task, availableRoles)
	if dec != nil {
		return dec
	}

	return local.DecideRole(ctx, task, availableRoles)
}

// DecideSubagent matches tasks to configured specialist sub-agents.
func (a *DecisionsFileAdapter) DecideSubagent(ctx context.Context, task string, candidates []SubagentCandidate) *SubagentDecision {
	a.mu.RLock()
	cfg := a.config
	router := a.router
	a.mu.RUnlock()

	if cfg.Mode == ModeLocalOnly || !cfg.EnableJev || router == nil || !router.IsEnabled() {
		return HeuristicSubagentDecision(task, candidates)
	}

	dec := router.DecideSubagent(ctx, task, candidates)
	if dec != nil {
		return dec
	}

	return HeuristicSubagentDecision(task, candidates)
}

// AssessToolSafety evaluates execution danger with RLCD risk scoring.
func (a *DecisionsFileAdapter) AssessToolSafety(toolName string, args map[string]any) SafetyAssessment {
	a.mu.RLock()
	local := a.localEngine
	a.mu.RUnlock()

	return local.AssessToolSafety(toolName, args)
}

// SuggestReflection provides localized recovery hints upon tool failure.
func (a *DecisionsFileAdapter) SuggestReflection(toolName string, args map[string]any, output string) string {
	a.mu.RLock()
	local := a.localEngine
	a.mu.RUnlock()

	return local.SuggestReflection(toolName, args, output)
}

// GetStats returns combined metrics from both the remote Jev router and the local small model.
func (a *DecisionsFileAdapter) GetStats() map[string]any {
	a.mu.RLock()
	router := a.router
	local := a.localEngine
	a.mu.RUnlock()

	res := make(map[string]any)
	if router != nil {
		for k, v := range router.GetStats() {
			res[k] = v
		}
	}
	if local != nil {
		neuralStats := local.NeuralEngine().GetStats()
		res["local_small_model"] = neuralStats
		res["local_total_decisions"] = neuralStats.TotalInferences
		res["local_avg_latency_us"] = neuralStats.AvgLatencyUs
	}
	return res
}
