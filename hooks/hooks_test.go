package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunBlocksOnJSONDecision(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		string(PreToolUse): []Matcher{{
			Matcher: "bash|Bash",
			Hooks:   []Command{{Type: "command", Command: `cat >/dev/null; echo '{"decision":"block","reason":"no rm"}'`}},
		}},
	}
	r := NewRunner(cfg, dir)
	res := r.Run(context.Background(), PreToolUse, "bash", map[string]any{"tool_input": map[string]any{"command": "rm -rf /"}})
	if !res.Blocked || res.Reason != "no rm" {
		t.Fatalf("got blocked=%v reason=%q", res.Blocked, res.Reason)
	}
}

func TestRunBlocksOnExitCode2(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		string(PreToolUse): []Matcher{{
			Matcher: "",
			Hooks:   []Command{{Type: "command", Command: `echo "denied by policy" >&2; exit 2`}},
		}},
	}
	r := NewRunner(cfg, dir)
	res := r.Run(context.Background(), PreToolUse, "edit", nil)
	if !res.Blocked || res.Reason != "denied by policy" {
		t.Fatalf("got blocked=%v reason=%q", res.Blocked, res.Reason)
	}
}

func TestMatcherSkipsNonMatchingTool(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		string(PreToolUse): []Matcher{{
			Matcher: "bash",
			Hooks:   []Command{{Command: `exit 2`}},
		}},
	}
	r := NewRunner(cfg, dir)
	res := r.Run(context.Background(), PreToolUse, "read", nil)
	if res.Blocked {
		t.Fatal("read should not match bash matcher")
	}
}

func TestLoadMergesFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"echo a"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"echo b"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg[string(Stop)]) != 2 {
		t.Fatalf("expected 2 matchers, got %d", len(cfg[string(Stop)]))
	}
}

func TestPostToolUseOutput(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		string(PostToolUse): []Matcher{{
			Matcher: "edit",
			Hooks:   []Command{{Command: `cat >/dev/null; echo formatted`}},
		}},
	}
	r := NewRunner(cfg, dir)
	res := r.Run(context.Background(), PostToolUse, "edit", nil)
	if res.Blocked {
		t.Fatal("post hook should not block")
	}
	if res.Output != "formatted" {
		t.Fatalf("output = %q", res.Output)
	}
}
