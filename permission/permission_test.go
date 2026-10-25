package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func meta(readOnly, needsApproval, destructive bool) ToolMeta {
	return ToolMeta{ReadOnly: readOnly, NeedsApproval: needsApproval, Destructive: destructive, SideEffectScope: "workspace"}
}

func TestCheckModes(t *testing.T) {
	read := meta(true, false, false)
	write := meta(false, true, false)
	write.SideEffectScope = "workspace"

	tests := []struct {
		name string
		mode Mode
		tool string
		m    ToolMeta
		want Action
	}{
		{"build allows read", ModeBuild, "read", read, ActionAllow},
		{"build asks write", ModeBuild, "write", write, ActionAsk},
		{"yolo allows write", ModeYolo, "write", write, ActionAllow},
		{"plan denies write", ModePlan, "write", write, ActionDeny},
		{"plan allows read", ModePlan, "read", read, ActionAllow},
		{"auto denies write", ModeAuto, "write", write, ActionDeny},
		{"edit allows edit", ModeEdit, "edit", write, ActionAllow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(tt.tool, tt.m, map[string]any{"file_path": "a.go"}, Context{Mode: tt.mode, PlanEnabled: tt.mode == ModePlan})
			if got.Action != tt.want {
				t.Fatalf("got %s, want %s (%s)", got.Action, tt.want, got.Message)
			}
		})
	}
}

func TestRulesPrecedence(t *testing.T) {
	write := meta(false, true, false)
	ctx := Context{
		Mode: ModeBuild,
		Rules: Ruleset{
			Deny:  []Rule{ParseRule("Bash(rm -rf:*)")},
			Allow: []Rule{ParseRule("Bash(go test:*)")},
		},
	}
	tests := []struct {
		cmd  string
		want Action
	}{
		{"rm -rf /", ActionDeny},
		{"go test ./...", ActionAllow},
		{"git push", ActionAsk},
	}
	for _, tt := range tests {
		got := Check("bash", ToolMeta{NeedsApproval: true, SideEffectScope: "workspace"}, map[string]any{"command": tt.cmd}, ctx)
		if got.Action != tt.want {
			t.Fatalf("%q: got %s, want %s", tt.cmd, got.Action, tt.want)
		}
	}
	_ = write
}

func TestParseRule(t *testing.T) {
	tests := map[string]Rule{
		"Bash":                {Tool: "Bash"},
		"Bash(git push:*)":    {Tool: "Bash", Pattern: "git push:*"},
		"Edit(src/**)":        {Tool: "Edit", Pattern: "src/**"},
		"  Bash(npm test)  ":  {Tool: "Bash", Pattern: "npm test"},
		"not a rule (broken)": {Tool: "not a rule", Pattern: "broken"},
	}
	for in, want := range tests {
		if got := ParseRule(in); got != want {
			t.Fatalf("%q: got %+v, want %+v", in, got, want)
		}
	}
}

func TestLoadSettingsMerge(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "settings.json")
	project := filepath.Join(dir, "project.json")
	if err := os.WriteFile(global, []byte(`{"mode":"build","permissions":{"allow":["Read"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{"permissions":{"deny":["Bash(rm -rf:*)"]},"disallowedTools":["web_fetch"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(global, project)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeBuild {
		t.Fatalf("mode = %q", s.Mode)
	}
	if len(s.Permissions.Allow) != 1 || len(s.Permissions.Deny) != 1 {
		t.Fatalf("rules not merged: %+v", s.Permissions)
	}
	if len(s.DisallowedTools) != 1 || s.DisallowedTools[0] != "web_fetch" {
		t.Fatalf("disallowed not merged: %v", s.DisallowedTools)
	}
}

func TestWildcardAndPrefix(t *testing.T) {
	if !contentMatches("src/**", "src/a/b.go") {
		t.Fatal("** should match nested path")
	}
	if !contentMatches("go test:*", "go test ./...") {
		t.Fatal("prefix should match")
	}
	if contentMatches("go test:*", "gotest") {
		t.Fatal("prefix must be a word boundary")
	}
	if !contentMatches("*.go", "main.go") {
		t.Fatal("* wildcard should match")
	}
}
