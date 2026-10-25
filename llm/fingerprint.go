package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PrefixFingerprint hashes the cacheable prefix: system messages plus tool
// definitions. Log it (RAKIT_DEBUG=1) to detect accidental cache busting.
func PrefixFingerprint(messages []LLMMessage, tools []ToolDefinition) string {
	h := sha256.New()
	for _, m := range messages {
		if m.Role != RoleSystem {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			h.Write([]byte(c))
		default:
			b, _ := json.Marshal(c)
			h.Write(b)
		}
		h.Write([]byte{'\n'})
	}
	b, _ := json.Marshal(tools)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
