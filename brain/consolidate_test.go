package brain

import (
	"strings"
	"testing"
	"time"
)

// syntheticTurn builds a representative finished turn: a user ask, tool work
// (one failure with a correction), assistant prose with a decision line, and
// code that must NOT leak into the fact log.
func syntheticTurn() TurnRecord {
	return TurnRecord{
		SessionID: "sess-42",
		TurnID:    "sess-42:msg-1",
		UserText:  "Add retry handling to the uploader",
		AssistantText: strings.Join([]string{
			"We decided to use exponential backoff for the retries.",
			"I'm going with a 5-attempt cap to bound worst-case latency.",
			"The function signature looks like this:",
			"```go",
			"func upload(ctx context.Context, data []byte) error { return nil }",
			"```",
			"Uploader now retries transient failures with exponential backoff.",
		}, "\n"),
		ToolTrace: []ToolTrace{
			{Name: "read", Args: map[string]any{"path": "/ws/uploader.go"}, Output: "func upload(ctx context.Context, data []byte) error {...}"},
			{Name: "edit", Args: map[string]any{"path": "/ws/uploader.go"}, Output: "applied patch"},
			{Name: "bash", IsError: true, Output: "exit 1: test timeouts after 2s"},
		},
		Files: []string{"/ws/uploader.go"},
		Corrections: []string{
			"The test failed because the fake clock never advanced, so the backoff timer waited forever.",
		},
		Objective:    "Make the uploader resilient to transient network failures",
		FinalSummary: "Added exponential-backoff retries to the uploader; all tests pass.",
		CompletedAt:  time.Now(),
	}
}

func TestConsolidateTurnExtractsFactsAndDigest(t *testing.T) {
	b := newTestBrain(t)
	rec := syntheticTurn()
	if err := ConsolidateTurn(b, rec); err != nil {
		t.Fatalf("ConsolidateTurn: %v", err)
	}

	facts := b.ValidFacts()
	var decisions, pitfalls int
	for _, f := range facts {
		switch f.Kind {
		case KindDecision:
			decisions++
		case KindPitfall:
			pitfalls++
		}
		if f.Source != rec.TurnID {
			t.Fatalf("fact source should carry the turn id, got %q", f.Source)
		}
		if strings.Contains(f.Text, "```") || strings.Contains(f.Text, "func upload") {
			t.Fatalf("code-derivable content leaked into facts: %q", f.Text)
		}
	}
	if decisions == 0 {
		t.Fatalf("expected decision facts, got %+v", facts)
	}
	if pitfalls == 0 {
		t.Fatalf("expected pitfall facts, got %+v", facts)
	}
	// Decision text carries the decided content.
	foundBackoff := false
	for _, f := range facts {
		if f.Kind == KindDecision && strings.Contains(f.Text, "exponential backoff") {
			foundBackoff = true
			if len(f.Refs) != 1 || f.Refs[0] != "/ws/uploader.go" {
				t.Fatalf("decision refs should carry touched files, got %+v", f.Refs)
			}
		}
		if f.Kind == KindPitfall && !strings.Contains(f.Text, "bash fails") {
			t.Fatalf("pitfall should name the failing tool, got %q", f.Text)
		}
	}
	if !foundBackoff {
		t.Fatalf("backoff decision missing from %+v", facts)
	}
	// The pitfall carries the "because" clause from the correction.
	for _, f := range facts {
		if f.Kind == KindPitfall && !strings.Contains(f.Text, "because") {
			t.Fatalf("pitfall should carry the causal clause, got %q", f.Text)
		}
	}

	// Digest written with objective, files, and outcome.
	digest, err := b.ReadDigest("sess-42")
	if err != nil {
		t.Fatalf("ReadDigest: %v", err)
	}
	for _, want := range []string{
		"transient network failures",
		"uploader.go",
		"exponential-backoff retries",
		"Add retry handling",
	} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest missing %q:\n%s", want, digest)
		}
	}
}

func TestConsolidateTurnIsIdempotent(t *testing.T) {
	b := newTestBrain(t)
	rec := syntheticTurn()
	if err := ConsolidateTurn(b, rec); err != nil {
		t.Fatalf("first run: %v", err)
	}
	beforeValid, beforeSuperseded := b.Counts()
	// Re-running the same turn (e.g. crash-recovery replay) dedupes to no-ops.
	if err := ConsolidateTurn(b, rec); err != nil {
		t.Fatalf("second run: %v", err)
	}
	afterValid, afterSuperseded := b.Counts()
	if beforeValid != afterValid || beforeSuperseded != afterSuperseded {
		t.Fatalf("re-consolidation mutated counts: %d/%d → %d/%d",
			beforeValid, beforeSuperseded, afterValid, afterSuperseded)
	}
}

func TestConsolidateTurnSkipsCodeLines(t *testing.T) {
	b := newTestBrain(t)
	rec := TurnRecord{
		SessionID: "sess-code",
		TurnID:    "sess-code:1",
		UserText:  "refactor",
		AssistantText: strings.Join([]string{
			"```",
			"if err := will use something(); err != nil {",
			"```",
			"Decided to split the parser into two passes.",
		}, "\n"),
		CompletedAt: time.Now(),
	}
	if err := ConsolidateTurn(b, rec); err != nil {
		t.Fatalf("ConsolidateTurn: %v", err)
	}
	for _, f := range b.ValidFacts() {
		if strings.Contains(f.Text, "err :=") {
			t.Fatalf("code line mined as a fact: %q", f.Text)
		}
	}
	if len(b.ValidFacts()) != 1 {
		t.Fatalf("expected exactly the prose decision, got %+v", b.ValidFacts())
	}
}

func TestConsolidateTurnNoCorrectionsNoPitfalls(t *testing.T) {
	b := newTestBrain(t)
	rec := TurnRecord{
		SessionID: "sess-clean",
		TurnID:    "sess-clean:1",
		UserText:  "list files",
		ToolTrace: []ToolTrace{{Name: "bash", IsError: true, Output: "exit 2"}},
		// No corrections → nothing causal to mine.
		CompletedAt: time.Now(),
	}
	if err := ConsolidateTurn(b, rec); err != nil {
		t.Fatalf("ConsolidateTurn: %v", err)
	}
	for _, f := range b.ValidFacts() {
		if f.Kind == KindPitfall {
			t.Fatalf("pitfall mined without a correction: %+v", f)
		}
	}
	// Digest still written so read_digest always has the latest state.
	if digest, _ := b.ReadDigest("sess-clean"); digest == "" {
		t.Fatal("digest should be written even for a bare turn")
	}
}
