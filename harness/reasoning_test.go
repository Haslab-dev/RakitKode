package harness

import (
	"strings"
	"testing"
)

func TestStreamReasoningSplitter(t *testing.T) {
	var textParts []string
	var reasoningParts []string

	splitter := NewStreamReasoningSplitter(
		func(text string) {
			textParts = append(textParts, text)
		},
		func(reasoning string) {
			reasoningParts = append(reasoningParts, reasoning)
		},
	)

	// Stream chunks simulating DeepSeek-R1 emitting <think>
	chunks := []string{
		"Hello! ",
		"<think>\nI should plan to ",
		"inspect the directory",
		" first.\n</think>",
		"Here is the directory listing:",
	}

	for _, c := range chunks {
		splitter.Feed(c)
	}
	splitter.Flush()

	fullText := strings.Join(textParts, "")
	fullReasoning := strings.Join(reasoningParts, "")

	expectedText := "Hello! Here is the directory listing:"
	expectedReasoning := "\nI should plan to inspect the directory first.\n"

	if fullText != expectedText {
		t.Errorf("fullText = %q, want %q", fullText, expectedText)
	}
	if fullReasoning != expectedReasoning {
		t.Errorf("fullReasoning = %q, want %q", fullReasoning, expectedReasoning)
	}

	if splitter.ReasoningChars() != len(expectedReasoning) {
		t.Errorf("ReasoningChars = %d, want %d", splitter.ReasoningChars(), len(expectedReasoning))
	}
}

func TestStreamReasoningSplitter_SplitTagAcrossChunks(t *testing.T) {
	var textParts []string
	var reasoningParts []string

	splitter := NewStreamReasoningSplitter(
		func(text string) {
			textParts = append(textParts, text)
		},
		func(reasoning string) {
			reasoningParts = append(reasoningParts, reasoning)
		},
	)

	// "<think>" split across two chunks: "<thi" + "nk>"
	splitter.Feed("<thi")
	splitter.Feed("nk>Reasoning text</thi")
	splitter.Feed("nk>Final answer")
	splitter.Flush()

	fullText := strings.Join(textParts, "")
	fullReasoning := strings.Join(reasoningParts, "")

	if fullText != "Final answer" {
		t.Errorf("fullText = %q, want 'Final answer'", fullText)
	}
	if fullReasoning != "Reasoning text" {
		t.Errorf("fullReasoning = %q, want 'Reasoning text'", fullReasoning)
	}
}
