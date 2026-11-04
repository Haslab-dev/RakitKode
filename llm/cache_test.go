package llm

import "testing"

func TestTokenStatsNormalize(t *testing.T) {
	tests := []struct {
		name       string
		in         TokenStats
		wantCached int
		wantPrompt int
	}{
		{"deepseek", TokenStats{PromptTokens: 1000, PromptCacheHitTokens: 900}, 900, 1000},
		{"openai nested", TokenStats{PromptTokens: 1000, PromptTokensDetails: PromptTokensDetails{CachedTokens: 700}}, 700, 1000},
		{"anthropic uncached input", TokenStats{PromptTokens: 146, CacheReadTokens: 830}, 830, 976},
		{"flat uncached prompt", TokenStats{PromptTokens: 146, CachedTokens: 830}, 830, 976},
		{"flat normal", TokenStats{PromptTokens: 1000, CachedTokens: 300}, 300, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.in
			s.Normalize()
			if s.CachedTokens != tt.wantCached {
				t.Fatalf("cached tokens = %d, want %d", s.CachedTokens, tt.wantCached)
			}
			if s.PromptTokens != tt.wantPrompt {
				t.Fatalf("prompt tokens = %d, want %d", s.PromptTokens, tt.wantPrompt)
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

func TestPreparePayloadAllModelsGetBreakpoints(t *testing.T) {
	messages := []LLMMessage{{Role: RoleSystem, Content: "s"}, {Role: RoleUser, Content: "u"}}
	rawMsgs, _ := PreparePayloadWithCache("deepseek", "deepseek-chat", messages, nil)
	msgMaps, ok := rawMsgs.([]map[string]any)
	if !ok {
		t.Fatalf("all providers and models should get cache breakpoints maps, got %T", rawMsgs)
	}
	if len(msgMaps) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgMaps))
	}
}

func TestPreparePayloadGatewayBreakpoints(t *testing.T) {
	messages := []LLMMessage{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "user prompt"},
	}
	// OpenRouter gateway should apply breakpoints for deepseek models
	rawMsgs, _ := PreparePayloadWithCache("openrouter", "deepseek-v4-1-flash", messages, nil)
	msgMaps, ok := rawMsgs.([]map[string]any)
	if !ok {
		t.Fatalf("openrouter payload should be transformed to maps with cache_control, got %T", rawMsgs)
	}
	found := false
	for _, m := range msgMaps {
		if parts, ok := m["content"].([]map[string]any); ok {
			for _, p := range parts {
				if _, ok := p["cache_control"]; ok {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("expected cache_control on openrouter deepseek-v4-1-flash")
	}

	// Agnes AI gateway should apply breakpoints for agnes and gpt-6-luna models
	rawAgnes, _ := PreparePayloadWithCache("prov-agnes", "gpt-6-luna", messages, nil)
	if _, ok := rawAgnes.([]map[string]any); !ok {
		t.Fatalf("agnes payload should be transformed to maps with cache_control, got %T", rawAgnes)
	}
}
