package jev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalDecisionEngine_EmbeddedRoute(t *testing.T) {
	lde := NewLocalDecisionEngine()
	tools := sampleTools()

	// 1. Explore query
	routed, dec := lde.Route(context.Background(), "list all files in the directory", tools)
	if dec == nil || dec.Category != "explore" {
		t.Fatalf("expected category 'explore', got %+v", dec)
	}
	if len(routed) == 0 {
		t.Fatal("expected non-empty routed tools for explore")
	}

	// 2. Git query
	_, dec = lde.Route(context.Background(), "show git diff and uncommitted changes", tools)
	if dec == nil || dec.Category != "git" {
		t.Fatalf("expected category 'git', got %+v", dec)
	}

	// 3. Casual chat query
	chatRouted, dec := lde.Route(context.Background(), "hello, how are you today?", tools)
	if dec == nil || dec.Category != "none" {
		t.Fatalf("expected category 'none' for chat, got %+v", dec)
	}
	// Casual chat must keep universal tools (not empty, not all)
	if len(chatRouted) == 0 || len(chatRouted) >= len(tools) {
		t.Fatalf("expected universal tools subset, got %d out of %d", len(chatRouted), len(tools))
	}
}

func TestLocalDecisionEngine_DecideRole(t *testing.T) {
	lde := NewLocalDecisionEngine()

	// 1. Research task
	roleDec := lde.DecideRole(context.Background(), "Audit codebase for potential security leaks and trace symbols", nil)
	if roleDec.Role != "research" {
		t.Fatalf("expected 'research', got %q (%s)", roleDec.Role, roleDec.Reason)
	}

	// 2. Planning task
	roleDec = lde.DecideRole(context.Background(), "Design architecture roadmap and decompose milestones for v2", nil)
	if roleDec.Role != "planning" {
		t.Fatalf("expected 'planning', got %q (%s)", roleDec.Role, roleDec.Reason)
	}

	// 3. Coding task
	roleDec = lde.DecideRole(context.Background(), "Fix bug in login handler and write unit tests", nil)
	if roleDec.Role != "coding" {
		t.Fatalf("expected 'coding', got %q (%s)", roleDec.Role, roleDec.Reason)
	}
}

func TestLocalDecisionEngine_AssessToolSafety(t *testing.T) {
	lde := NewLocalDecisionEngine()

	// 1. Read-only tool should be safe
	safety := lde.AssessToolSafety("read", map[string]any{"path": "main.go"})
	if safety.Risk != RiskSafe || !safety.AutoApproveAllowed {
		t.Fatalf("expected read to be RiskSafe & auto-approved, got %+v", safety)
	}

	// 2. Benign terminal command
	safety = lde.AssessToolSafety("bash", map[string]any{"command": "git status"})
	if safety.Risk != RiskMedium && safety.Risk != RiskLow {
		t.Fatalf("expected git status to be Low/Medium risk, got %+v", safety)
	}

	// 3. Dangerous / destructive command
	safety = lde.AssessToolSafety("bash", map[string]any{"command": "rm -rf / --no-preserve-root"})
	if safety.Risk != RiskCritical || safety.AutoApproveAllowed {
		t.Fatalf("expected rm -rf / to be RiskCritical and NOT auto-approved, got %+v", safety)
	}

	// 4. Sensitive file edit
	safety = lde.AssessToolSafety("write", map[string]any{"path": ".env"})
	if safety.Risk != RiskHigh || safety.AutoApproveAllowed {
		t.Fatalf("expected .env write to be RiskHigh and require confirmation, got %+v", safety)
	}
}

func TestLocalDecisionEngine_SuggestReflection(t *testing.T) {
	lde := NewLocalDecisionEngine()

	refl := lde.SuggestReflection("edit", map[string]any{"path": "file.go"}, "Error: target text not found in file")
	if refl == "" {
		t.Fatal("expected reflection advice for edit text mismatch")
	}

	refl = lde.SuggestReflection("bash", map[string]any{"command": "foo"}, "bash: command not found: foo")
	if refl == "" {
		t.Fatal("expected reflection advice for command not found")
	}
}

func TestLocalDecisionEngine_SimulatedSmallModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"category\": \"modify\", \"needs_tools\": 0.95, \"confidence\": 0.98}"
				}
			}]
		}`))
	}))
	defer ts.Close()

	lde := NewLocalDecisionEngine()
	lde.SetLocalModel(ts.URL, "test-small-model", "")

	tools := sampleTools()
	routed, dec := lde.Route(context.Background(), "edit the main function", tools)
	if dec == nil || dec.Category != "modify" {
		t.Fatalf("expected category 'modify' from small model, got %+v", dec)
	}
	if len(routed) == 0 {
		t.Fatal("expected non-empty routed tools for modify")
	}

	stats := lde.GetStats()
	if stats.LocalModelHits != 1 {
		t.Fatalf("expected 1 LocalModelHit, got %d", stats.LocalModelHits)
	}
}
