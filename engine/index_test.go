package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSymbolSearchTool(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	src := "package main\n\nfunc HelloWorld() {}\n\ntype Widget struct{}\n"
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{Home: home, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.Tools().Execute(context.Background(), "symbol_search", `{"query":"HelloWorld"}`)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result type %T", res)
	}
	if count, _ := m["count"].(int); count == 0 {
		t.Fatalf("expected a symbol, got %+v", m)
	}
	syms, _ := m["symbols"].([]map[string]any)
	if len(syms) == 0 || syms[0]["name"] != "HelloWorld" {
		t.Fatalf("unexpected symbols: %+v", syms)
	}

	// Prefix fallback.
	res, err = e.Tools().Execute(context.Background(), "symbol_search", `{"query":"Wid"}`)
	if err != nil {
		t.Fatal(err)
	}
	m = res.(map[string]any)
	if count, _ := m["count"].(int); count == 0 {
		t.Fatalf("expected prefix match, got %+v", m)
	}
}
