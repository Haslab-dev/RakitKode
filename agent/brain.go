package agent

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Haslab-dev/RakitKode/brain"
)

// SetBrain attaches the living-memory store. nil disables brain features
// (the default: hosts opt in via ManagerOptions.Brain or this setter).
func (m *Manager) SetBrain(b *brain.Brain) {
	m.mu.Lock()
	m.brainMgr = b
	m.mu.Unlock()
}

// Brain returns the attached living-memory store (may be nil).
func (m *Manager) Brain() *brain.Brain {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.brainMgr
}

func (m *Manager) brainSnapshot() *brain.Brain {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.brainMgr
}

// buildBrainContext renders the <project_brain> block for the turn's cached
// prefix, preferring facts applicable to the current user request. Called
// once per turn (not per step) so the prefix stays byte-identical across
// steps for provider prompt caching.
func buildBrainContext(b *brain.Brain, sess *Session) string {
	if b == nil || sess == nil {
		return ""
	}
	userText := ""
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			userText = sess.Messages[i].Text()
			break
		}
	}
	if strings.TrimSpace(userText) == "" {
		// No conversational anchor: no applicability signal, so do not
		// blind-inject memory (experience-following research: untargeted
		// recall can mislead).
		return ""
	}
	return b.BriefFor([]string{userText}, nil, nil)
}

// consolidateBrain runs on the async consolidation path after a successful
// turn: it builds a brain.TurnRecord from the turn's transcript slice and
// lets the brain package mine durable facts deterministically (no LLM call).
// All panics are contained — consolidation must never take a turn down.
func (m *Manager) consolidateBrain(sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent:%s] brain consolidation panic: %v", sessionID, r)
		}
	}()
	br := m.brainSnapshot()
	if br == nil {
		return
	}
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	if !ok || sess.Folder == "" {
		m.mu.RUnlock()
		return
	}
	rec := buildTurnRecord(sess)
	m.mu.RUnlock()

	if err := brain.ConsolidateTurn(br, rec); err != nil {
		log.Printf("[agent:%s] brain consolidation: %v", sessionID, err)
	}
}

// buildTurnRecord slices the messages belonging to the most recent turn
// (everything after the last user message, plus that prompt) into the
// neutral record brain.ConsolidateTurn mines.
func buildTurnRecord(sess *Session) brain.TurnRecord {
	rec := brain.TurnRecord{
		SessionID:   sess.ID,
		CompletedAt: time.Now(),
	}
	// Locate the last user message: the turn boundary.
	start := len(sess.Messages)
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			start = i
			break
		}
	}
	if start < len(sess.Messages) && sess.Messages[start].Role == "user" {
		rec.UserText = sess.Messages[start].Text()
		rec.TurnID = sess.ID + ":" + sess.Messages[start].ID
	}

	files := map[string]bool{}
	afterFailure := false
	for _, msg := range sess.Messages[start:] {
		switch msg.Role {
		case "assistant":
			var texts []string
			for _, b := range msg.Content {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						texts = append(texts, b.Text)
					}
				case "tool_use":
					for _, k := range []string{"path", "file", "target_file", "file_path", "filepath", "filePath"} {
						if p, ok := b.Arguments[k].(string); ok && p != "" && !files[p] {
							files[p] = true
						}
					}
				}
			}
			if len(texts) > 0 {
				text := strings.Join(texts, "\n")
				rec.AssistantText += text + "\n"
				if afterFailure {
					// The prose that follows a failed tool call is the
					// correction feeding pitfall mining.
					rec.Corrections = append(rec.Corrections, text)
					afterFailure = false
				}
				rec.FinalSummary = text
			}
		case "tool":
			for _, b := range msg.Content {
				if b.Type != "tool_result" {
					continue
				}
				rec.ToolTrace = append(rec.ToolTrace, brain.ToolTrace{
					Name:    b.Name,
					IsError: b.IsError,
					Output:  truncateStr(b.Text, 200),
				})
				if b.IsError {
					afterFailure = true
				}
			}
		}
	}
	for f := range files {
		rec.Files = append(rec.Files, f)
	}
	if p := sess.Progress; p.CurrentGoal != "" {
		rec.Objective = p.CurrentGoal
		rec.CompletedSteps = p.CompletedSteps
	}
	return rec
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Brain bridge methods (SessionBrain tools) on sessionBridge.

// WriteBrainNote stores a freeform note in the project brain.
func (b *sessionBridge) WriteBrainNote(title string, body string) (map[string]any, error) {
	br := b.m.brainSnapshot()
	if br == nil {
		return nil, fmt.Errorf("brain store not initialized (host did not enable Brain)")
	}
	path, err := br.WriteNote(title, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "status": "written"}, nil
}

// SearchBrain returns the top valid facts matching a query.
func (b *sessionBridge) SearchBrain(query string, limit int) ([]map[string]any, error) {
	br := b.m.brainSnapshot()
	if br == nil {
		return nil, fmt.Errorf("brain store not initialized (host did not enable Brain)")
	}
	if limit <= 0 {
		limit = 8
	}
	facts := br.Search(query, limit)
	out := make([]map[string]any, 0, len(facts))
	for _, f := range facts {
		out = append(out, map[string]any{
			"id":    f.ID,
			"kind":  f.Kind,
			"text":  f.Text,
			"date":  f.Date(),
			"refs":  f.Refs,
			"valid": f.Valid(),
		})
	}
	return out, nil
}

// ReadBrainDigest returns a session digest; empty id means the latest one.
func (b *sessionBridge) ReadBrainDigest(sessionID string) (map[string]any, error) {
	br := b.m.brainSnapshot()
	if br == nil {
		return nil, fmt.Errorf("brain store not initialized (host did not enable Brain)")
	}
	if sessionID == "" {
		id, content, err := br.LatestDigest()
		if err != nil {
			return nil, err
		}
		return map[string]any{"session_id": id, "content": content}, nil
	}
	content, err := br.ReadDigest(sessionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": sessionID, "content": content}, nil
}
