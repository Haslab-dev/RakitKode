package message

import "testing"

// TestReasoningAndTextStaySeparate verifies reasoning deltas do not leak
// into the text part and vice versa (regression: text appended into the
// reasoning part made the TUI merge thinking with the answer).
func TestReasoningAndTextStaySeparate(t *testing.T) {
	t.Parallel()
	m := Message{Role: Assistant}
	m.AppendReasoningContent("step one. ")
	m.AppendReasoningContent("step two.")
	m.AppendContent("answer text")

	if got := m.ReasoningContent().Thinking; got != "step one. step two." {
		t.Fatalf("reasoning = %q", got)
	}
	if got := m.Content().Text; got != "answer text" {
		t.Fatalf("text = %q, want separate from reasoning", got)
	}
}
