package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverWorkspaceRules(t *testing.T) {
	dir := t.TempDir()

	// Create sample AGENTS.md and .cursorrules
	agentsMd := "# AGENTS\n- Follow conventional commits\n- Use Go 1.26"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMd), 0644); err != nil {
		t.Fatal(err)
	}

	cursorRules := "Prefer concise answers without fluff"
	if err := os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte(cursorRules), 0644); err != nil {
		t.Fatal(err)
	}

	rules, prompt := DiscoverWorkspaceRules(dir)
	if len(rules) != len(KnownRuleFilePaths) {
		t.Errorf("got %d rules, want %d", len(rules), len(KnownRuleFilePaths))
	}

	if !strings.Contains(prompt, "Follow conventional commits") {
		t.Errorf("prompt missing AGENTS.md content: %s", prompt)
	}
	if !strings.Contains(prompt, "Prefer concise answers") {
		t.Errorf("prompt missing .cursorrules content: %s", prompt)
	}

	// Test SystemPromptBuilder integration
	builder := SystemPromptBuilder{
		WorkspaceDir:      dir,
		AutoDiscoverRules: true,
	}
	built := builder.Build()
	if !strings.Contains(built, "# Project Rules & Coding Instructions") {
		t.Errorf("built prompt missing rules section: %s", built)
	}
}
