package tools

import (
	"context"
	"testing"
)

func TestToolSearch(t *testing.T) {
	reg := NewRegistry(nil)

	spec, ok := reg.Lookup("tool_search")
	if !ok {
		t.Fatal("tool_search not registered")
	}

	ctx := context.Background()

	// 1. Search for bash
	res, err := spec.Handler(ctx, map[string]any{"query": "bash"})
	if err != nil {
		t.Fatalf("tool_search handler failed: %v", err)
	}

	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any result, got %T", res)
	}

	count, _ := m["count"].(int)
	if count == 0 {
		t.Fatal("expected at least 1 match for 'bash'")
	}

	matches, _ := m["matches"].([]map[string]any)
	foundBash := false
	for _, match := range matches {
		if match["name"] == "bash" {
			foundBash = true
			break
		}
	}
	if !foundBash {
		t.Errorf("expected matches to include 'bash', got %+v", matches)
	}

	// 2. Search for index
	resIndex, err := spec.Handler(ctx, map[string]any{"query": "index"})
	if err != nil {
		t.Fatalf("search for 'index' failed: %v", err)
	}
	mIndex, _ := resIndex.(map[string]any)
	if countIndex, _ := mIndex["count"].(int); countIndex == 0 {
		t.Fatal("expected at least 1 match for 'index'")
	}
}
