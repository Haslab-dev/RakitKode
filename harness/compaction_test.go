package harness

import (
	"strings"
	"testing"
)

func TestSmartTruncateToolOutput(t *testing.T) {
	// Small output should remain untouched
	small := "Line 1\nLine 2\nLine 3"
	res := SmartTruncateToolOutput(small, 10, 1000)
	if res != small {
		t.Errorf("got %q, want %q", res, small)
	}

	// Large output exceeding lines
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = strings.Repeat("x", 20)
	}
	large := strings.Join(lines, "\n")

	truncated := SmartTruncateToolOutput(large, 30, 50000)
	if !strings.Contains(truncated, "omitted for context efficiency") {
		t.Errorf("expected omission message in truncated output")
	}
	if !strings.HasPrefix(truncated, lines[0]) {
		t.Errorf("expected head part to start with first line")
	}
	if !strings.HasSuffix(truncated, lines[99]) {
		t.Errorf("expected tail part to end with last line")
	}
}

func TestSemanticSummarizeToolResult(t *testing.T) {
	// 1. Read
	s := SemanticSummarizeToolResult("read_file", map[string]any{"path": "pkg/foo.go"}, "line1\nline2\nline3", true)
	if !strings.Contains(s, `read "pkg/foo.go"`) || !strings.Contains(s, "3 lines") {
		t.Errorf("unexpected read summary: %s", s)
	}

	// 2. Write
	s = SemanticSummarizeToolResult("write", map[string]any{"path": "cmd/main.go"}, "ok", true)
	if !strings.Contains(s, `write "cmd/main.go"`) {
		t.Errorf("unexpected write summary: %s", s)
	}

	// 3. Bash
	s = SemanticSummarizeToolResult("bash", map[string]any{"command": "go test ./..."}, "PASS\nok", true)
	if !strings.Contains(s, `bash "go test ./..."`) {
		t.Errorf("unexpected bash summary: %s", s)
	}
}

func TestPruneReasoning(t *testing.T) {
	input := "<think>\nLet me think about how to solve this.\nFirst step...\n</think>\nHere is the answer."
	cleaned, ok := PruneReasoning(input)
	if !ok {
		t.Fatal("expected prune reasoning to report ok=true")
	}
	if strings.Contains(cleaned, "<think>") || strings.Contains(cleaned, "Let me think") {
		t.Fatalf("thinking block was not stripped: %s", cleaned)
	}
	if cleaned != "Here is the answer." {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}

	// Message without thinking block
	noThink := "Just plain output."
	res, changed := PruneReasoning(noThink)
	if changed || res != noThink {
		t.Fatalf("expected unchanged for non-thinking content")
	}
}

func TestCompactionDecisionSet(t *testing.T) {
	ds := NewCompactionDecisionSet()
	if ds.IsCompacted(1) {
		t.Fatal("expected false for uncompacted index")
	}
	ds.Record(1, "micro")
	if !ds.IsCompacted(1) {
		t.Fatal("expected true for compacted index 1")
	}
	if ds.IsCompacted(2) {
		t.Fatal("expected false for index 2")
	}
}
