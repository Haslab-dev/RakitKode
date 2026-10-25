package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildFileIndexSkipsVendored(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a.go", "src/b.go", "node_modules/x.js", ".git/config"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := buildFileIndex(root)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	for _, f := range files {
		if f != "a.go" && f != filepath.Join("src", "b.go") {
			t.Fatalf("unexpected file %q", f)
		}
	}
}

func TestCompleteAtMention(t *testing.T) {
	m := New(nil, "s")
	m.atOpen = true
	m.atStart = 3
	m.atItems = []string{"src/main.go"}
	m.atSelected = 0
	m.textarea.SetValue("hi @ma")
	m.completeAtMention()
	if got := m.textarea.Value(); got != "hi @src/main.go " {
		t.Fatalf("value = %q", got)
	}
	if m.atOpen {
		t.Fatal("popup should close")
	}
}
