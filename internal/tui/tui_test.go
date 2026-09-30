package tui

import (
	"testing"

	"github.com/Haslab-dev/RakitKode/internal/app"
	"github.com/Haslab-dev/RakitKode/internal/message"
)

// newHeadlessModel returns a Model without an app for transcript tests.
func newHeadlessModel() Model {
	return Model{
		streamBlocks: map[string]*streamBlocks{},
		toolLines:    map[string]int{},
		toolResolved: map[string]bool{},
		status:       "ready",
	}
}

// TestStreamingBlocksDeduplicated verifies that repeated message-update
// events (the service republishes the whole message per delta) do not
// duplicate reasoning or text blocks.
func TestStreamingBlocksDeduplicated(t *testing.T) {
	t.Parallel()
	m := newHeadlessModel()

	evolve := func(thinking, text string) {
		msg := message.Message{
			ID:   "msg-1",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: thinking},
				message.TextContent{Text: text},
			},
		}
		m.handleMessageEvent(app.MessageUpdateMsg{Type: "updated", Payload: msg})
	}

	evolve("thinking part 1", "")
	evolve("thinking part 1. more", "ans")
	evolve("thinking part 1. more", "answer done")

	if n := len(m.transcript); n != 2 {
		t.Fatalf("expected 2 blocks (reasoning+text), got %d: %#v", n, m.transcript)
	}
	if got := m.transcript[0].content; got != "thinking part 1. more" {
		t.Fatalf("reasoning = %q", got)
	}
	if got := m.transcript[1].content; got != "answer done" {
		t.Fatalf("text = %q", got)
	}
}

// TestToolLineResolvedOnce verifies a tool result marks the line done and
// a later republished tool call does not clobber the ✓ back to "…".
func TestToolLineResolvedOnce(t *testing.T) {
	t.Parallel()
	m := newHeadlessModel()

	pending := message.Message{
		ID:   "msg-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call-1", Name: "view", Finished: false},
		},
	}
	m.handleMessageEvent(app.MessageUpdateMsg{Type: "updated", Payload: pending})

	result := message.Message{
		ID:   "msg-tool",
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "call-1", Name: "view", Content: "file body"},
		},
	}
	m.handleMessageEvent(app.MessageUpdateMsg{Type: "updated", Payload: result})

	// Republished assistant message still carries the tool call.
	m.handleMessageEvent(app.MessageUpdateMsg{Type: "updated", Payload: pending})

	if !m.toolResolved["call-1"] {
		t.Fatal("call-1 should be resolved")
	}
	line := m.transcript[m.toolLines["call-1"]].content
	if !contains(line, "✓") {
		t.Fatalf("resolved line lost ✓ after republish: %q", line)
	}
	if contains(line, "…") && !contains(line, "✓") {
		t.Fatalf("pending marker overwrote result: %q", line)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
