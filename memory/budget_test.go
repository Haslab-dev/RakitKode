package memory

import (
	"strings"
	"testing"
)

func TestFormatPromptBudget(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.SetGlobalDir(dir)
	for _, e := range []Entry{
		{Key: "prefer-tabs", Content: "Always use tabs for Go files", Category: "preference", Scope: "global"},
		{Key: "no-generated", Content: "Never commit generated files", Category: "preference", Scope: "global"},
	} {
		if err := m.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	full := m.FormatPromptBudget(0)
	if !strings.Contains(full, "prefer-tabs") || !strings.Contains(full, "no-generated") {
		t.Fatalf("full prompt missing entries: %q", full)
	}
	small := m.FormatPromptBudget(120)
	if len(small) > 200 {
		t.Fatalf("budget not enforced: %d chars", len(small))
	}
	if !strings.Contains(small, "more memories omitted") {
		t.Fatalf("expected omission marker: %q", small)
	}
}
