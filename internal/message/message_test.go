package message

import "testing"

// TestPartsRoundTrip verifies tagged JSON serialization preserves the
// concrete part types.
func TestPartsRoundTrip(t *testing.T) {
	t.Parallel()
	original := []ContentPart{
		TextContent{Text: "hello"},
		ReasoningContent{Thinking: "hmm", Signature: "sig"},
		ToolCall{ID: "call1", Name: "view", Input: "{}", Finished: true},
		ToolResult{ToolCallID: "call1", Name: "view", Content: "contents", IsError: false},
		Finish{Reason: FinishReasonEndTurn},
	}

	data, err := marshalParts(original)
	if err != nil {
		t.Fatalf("marshalParts: %v", err)
	}
	decoded, err := unmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshalParts: %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("expected %d parts, got %d", len(original), len(decoded))
	}

	text, ok := decoded[0].(TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("part 0: expected TextContent{hello}, got %#v", decoded[0])
	}
	reasoning, ok := decoded[1].(ReasoningContent)
	if !ok || reasoning.Signature != "sig" {
		t.Fatalf("part 1: expected ReasoningContent, got %#v", decoded[1])
	}
	call, ok := decoded[2].(ToolCall)
	if !ok || call.Name != "view" || !call.Finished {
		t.Fatalf("part 2: expected ToolCall, got %#v", decoded[2])
	}
	result, ok := decoded[3].(ToolResult)
	if !ok || result.Content != "contents" {
		t.Fatalf("part 3: expected ToolResult, got %#v", decoded[3])
	}
	finish, ok := decoded[4].(Finish)
	if !ok || finish.Reason != FinishReasonEndTurn {
		t.Fatalf("part 4: expected Finish, got %#v", decoded[4])
	}
}

// TestToAIMessageOrdering verifies user/assistant/tool conversions keep
// tool results adjacent to their calls.
func TestToAIMessageOrdering(t *testing.T) {
	t.Parallel()
	assistant := Message{
		Role: Assistant,
		Parts: []ContentPart{
			ToolCall{ID: "call1", Name: "view", Input: "{}", Finished: true},
		},
	}
	msgs := assistant.ToAIMessage()
	if len(msgs) != 1 || msgs[0].Role != "assistant" {
		t.Fatalf("expected one assistant message, got %#v", msgs)
	}
	tool := Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{ToolCallID: "call1", Content: "ok"},
		},
	}
	msgs = tool.ToAIMessage()
	if len(msgs) != 1 || msgs[0].Role != "tool" {
		t.Fatalf("expected one tool message, got %#v", msgs)
	}
}
