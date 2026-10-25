package llm

import "testing"

func TestPrefixFingerprintStability(t *testing.T) {
	tools := []ToolDefinition{{Type: "function", Function: FunctionSpec{Name: "read"}}}
	a := []LLMMessage{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "hello"}}
	b := []LLMMessage{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "different"}}
	if PrefixFingerprint(a, tools) != PrefixFingerprint(b, tools) {
		t.Fatal("user content must not affect the prefix fingerprint")
	}
	c := []LLMMessage{{Role: RoleSystem, Content: "changed"}, {Role: RoleUser, Content: "hello"}}
	if PrefixFingerprint(a, tools) == PrefixFingerprint(c, tools) {
		t.Fatal("system change must change the fingerprint")
	}
	if PrefixFingerprint(a, tools) == PrefixFingerprint(a, nil) {
		t.Fatal("tools change must change the fingerprint")
	}
}
