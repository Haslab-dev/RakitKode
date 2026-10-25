package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchContentWithPathScopesResults(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("needle root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "sub.txt"), []byte("needle sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSearchManager()
	sm.SetDirectories([]string{root})

	results, err := sm.SearchContentWithOptions(SearchOptions{Query: "needle", Path: "sub", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results scoped to sub/")
	}
	for _, r := range results {
		if filepath.Base(filepath.Dir(r.Path)) != "sub" {
			t.Fatalf("result escaped scoped path: %s", r.Path)
		}
	}
}
