package tui

import (
	"strings"
	"testing"
)

func TestRenderDiff(t *testing.T) {
	m := Model{width: 100}
	out := m.renderDiff("--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old line\n+new line\n", false)
	if !strings.Contains(out, "new line") || !strings.Contains(out, "old line") {
		t.Fatalf("diff body missing: %q", out)
	}
	if !strings.Contains(out, "+1 −1") {
		t.Fatalf("diff summary missing: %q", out)
	}
}

func TestRenderDiffCollapsedLimitsLines(t *testing.T) {
	m := Model{width: 100}
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("+line\n")
	}
	out := m.renderDiff(b.String(), false)
	if !strings.Contains(out, "more (ctrl+o expands)") {
		t.Fatalf("expected truncation notice: %q", out)
	}
}
