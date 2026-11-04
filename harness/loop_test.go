package harness

import (
	"strings"
	"testing"
)

type mockAdvisor struct {
	reflection string
}

func (m *mockAdvisor) SuggestReflection(toolName string, args map[string]any, output string) string {
	return m.reflection
}

func TestTurnLoopGuard(t *testing.T) {
	guard := NewTurnLoopGuard()

	call := ToolCall{
		Name:  "bash",
		Input: map[string]any{"command": "npm test"},
	}
	resFail := ToolResult{
		Success: false,
		Content: "FAIL: test timed out",
	}

	// 1st call
	isBreaker, warning := guard.Record(call, resFail)
	if isBreaker || warning != "" {
		t.Fatalf("unexpected loop signal on 1st call: breaker=%v, warning=%v", isBreaker, warning)
	}

	// 2nd call
	isBreaker, warning = guard.Record(call, resFail)
	if isBreaker || warning != "" {
		t.Fatalf("unexpected loop signal on 2nd call: breaker=%v, warning=%v", isBreaker, warning)
	}

	// 3rd call: should trigger warning
	isBreaker, warning = guard.Record(call, resFail)
	if isBreaker {
		t.Fatal("breaker should not trip on 3rd call")
	}
	if warning == "" {
		t.Fatal("expected warning on 3rd identical call")
	}

	// 4th and 5th call
	guard.Record(call, resFail)
	guard.Record(call, resFail)

	// 6th call: should trip breaker
	isBreaker, _ = guard.Record(call, resFail)
	if !isBreaker {
		t.Fatal("expected breaker to trip on 6th identical call")
	}
}

func TestTurnLoopGuard_WithDecisionAdvisor(t *testing.T) {
	guard := NewTurnLoopGuard()
	advisor := &mockAdvisor{
		reflection: "Check if the test command needs extra flags or if dependencies need installing.",
	}

	call := ToolCall{
		Name:  "bash",
		Input: map[string]any{"command": "npm test"},
	}
	resFail := ToolResult{
		Success: false,
		Content: "FAIL: command not found",
	}

	guard.Record(call, resFail, advisor)
	guard.Record(call, resFail, advisor)

	// 3rd call: should trigger Pandu-Jev smart reflection warning
	isBreaker, warning := guard.Record(call, resFail, advisor)
	if isBreaker {
		t.Fatal("breaker should not trip on 3rd call")
	}
	if !strings.Contains(warning, "Pandu-Jev Smart Decision Reflection") {
		t.Fatalf("expected Pandu-Jev smart reflection in warning, got: %s", warning)
	}
	if !strings.Contains(warning, "dependencies need installing") {
		t.Fatalf("expected custom reflection advice in warning, got: %s", warning)
	}
}
