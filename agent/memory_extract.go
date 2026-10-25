package agent

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/Haslab-dev/RakitKode/memory"
)

// extractPreferenceClauses finds durable user preferences worth remembering.
// It is rule-based on purpose: extraction must not add an LLM call to every
// turn.
func extractPreferenceClauses(text string) []string {
	keywords := []string{"always ", "never ", "prefer ", "remember ", "remember:", "use ", "don't use ", "do not use "}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		matched := false
		for _, k := range keywords {
			if strings.Contains(lower, k) {
				if k == "use " && !strings.Contains(lower, " instead of ") {
					continue
				}
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// Trim a trailing sentence fragment and bound the length.
		if idx := strings.IndexAny(trimmed, ".!?"); idx > 10 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		if len(trimmed) < 8 || len(trimmed) > 240 {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return out
}

func memoryKey(content string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(content))))
	return fmt.Sprintf("auto-%08x", h.Sum32())
}

// extractMemories mines the last user turn for durable preferences and stores
// them in workspace memory. Existing identical memories are left untouched.
func (m *Manager) extractMemories(sessionID string) {
	if m.memoryMgr == nil {
		return
	}
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.RUnlock()
		return
	}
	folder := sess.Folder
	var userText string
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			userText = sess.Messages[i].Text()
			break
		}
	}
	m.mu.RUnlock()

	if strings.TrimSpace(userText) == "" {
		return
	}
	if folder != "" {
		m.memoryMgr.SetWorkspace(folder)
	}
	for _, clause := range extractPreferenceClauses(userText) {
		existing := m.memoryMgr.Search(clause)
		found := false
		for _, e := range existing {
			if strings.EqualFold(strings.TrimSpace(e.Content), clause) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		_ = m.memoryMgr.Save(memory.Entry{
			Key:      memoryKey(clause),
			Content:  clause,
			Category: "preference",
			Scope:    "workspace",
		})
	}
}
