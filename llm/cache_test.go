package llm

import "testing"

func TestTokenStatsNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   TokenStats
		want int
	}{
		{"deepseek", TokenStats{PromptCacheHitTokens: 900}, 900},
		{"openai nested", TokenStats{PromptTokensDetails: PromptTokensDetails{CachedTokens: 700}}, 700},
		{"anthropic", TokenStats{CacheReadTokens: 500}, 500},
		{"flat", TokenStats{CachedTokens: 300}, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.in
			s.Normalize()
			if s.CachedTokens != tt.want {
				t.Fatalf("got %d, want %d", s.CachedTokens, tt.want)
			}
		})
	}
}

func TestPreparePayloadAnthropicBreakpoints(t *testing.T) {
	messages := []LLMMessage{
		{Role: RoleSystem, Content: "system instructions"},
		{Role: RoleUser, Content: "first"},
		{Role: RoleAssistant, Content: "answer"},
		{Role: RoleUser, Content: "second"},
		{Role: RoleAssistant, Content: "answer two"},
		{Role: RoleTool, Content: "tool result"},
	}
	tools := []ToolDefinition{{Type: "function", Function: FunctionSpec{Name: "read"}}}
	rawMsgs, rawTools := PreparePayloadWithCache("anthropic", "claude-sonnet-4", messages, tools)

	msgMaps, ok := rawMsgs.([]map[string]any)
	if !ok {
		t.Fatalf("messages type %T", rawMsgs)
	}
	toolMaps, ok := rawTools.([]map[string]any)
	if !ok {
		t.Fatalf("tools type %T", rawTools)
	}
	count := 0
	if _, ok := toolMaps[0]["cache_control"]; ok {
		count++
	}
	for _, m := range msgMaps {
		switch c := m["content"].(type) {
		case []map[string]any:
			for _, part := range c {
				if _, ok := part["cache_control"]; ok {
					count++
				}
			}
		}
	}
	if count != 4 {
		t.Fatalf("cache breakpoints = %d, want 4", count)
	}
}

func TestPreparePayloadDeepSeekHasNoBreakpoints(t *testing.T) {
	messages := []LLMMessage{{Role: RoleSystem, Content: "s"}, {Role: RoleUser, Content: "u"}}
	rawMsgs, rawTools := PreparePayloadWithCache("deepseek", "deepseek-chat", messages, nil)
	msgMaps, ok := rawMsgs.([]LLMMessage)
	if !ok {
		t.Fatalf("deepseek payload should be unchanged, got %T", rawMsgs)
	}
	if msgMaps[0].Content != "s" {
		t.Fatal("content mutated")
	}
	if ts, ok := rawTools.([]ToolDefinition); !ok || len(ts) != 0 {
		t.Fatalf("tools should stay empty, got %#v", rawTools)
	}
}
