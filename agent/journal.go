package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Haslab-dev/RakitKode/tools"
)

// turnJournal persists enough state to resume an interrupted turn after a
// crash, a quit, or a killed process. It never stores model output; resuming
// always asks for approval before executing any pending tool call.
type turnJournal struct {
	SessionID        string              `json:"session_id"`
	State            SessionState        `json:"state"`
	Step             int                 `json:"step"`
	Model            string              `json:"model,omitempty"`
	PendingTools     []ContentBlock      `json:"pending_tools,omitempty"`
	PendingQuestions []tools.AskQuestion `json:"pending_questions,omitempty"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

func (m *Manager) journalPath(sessionID string) string {
	if m.storePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.storePath), "sessions", sessionID, "turn.json")
}

// writeJournalLocked persists the current turn state. Caller holds m.mu.
func (m *Manager) writeJournalLocked(sess *Session, step int) {
	path := m.journalPath(sess.ID)
	if path == "" {
		return
	}
	if sess.State == StateIdle {
		_ = os.Remove(path)
		return
	}
	j := turnJournal{
		SessionID:        sess.ID,
		State:            sess.State,
		Step:             step,
		Model:            sess.Model,
		PendingTools:     sess.PendingTools,
		PendingQuestions: sess.PendingQuestions,
		UpdatedAt:        time.Now(),
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// clearJournal removes the journal for a session.
func (m *Manager) clearJournal(sessionID string) {
	if path := m.journalPath(sessionID); path != "" {
		_ = os.Remove(path)
	}
}

// readJournal loads a journal without locking.
func (m *Manager) readJournal(sessionID string) *turnJournal {
	path := m.journalPath(sessionID)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var j turnJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil
	}
	if j.State == "" || j.State == StateIdle {
		return nil
	}
	return &j
}

// applyJournal restores an interrupted turn onto a loaded session.
func (m *Manager) applyJournal(s *Session) bool {
	j := m.readJournal(s.ID)
	if j == nil {
		return false
	}
	s.State = j.State
	if len(j.PendingTools) > 0 {
		s.PendingTools = j.PendingTools
	}
	if len(j.PendingQuestions) > 0 {
		s.PendingQuestions = j.PendingQuestions
	}
	s.Interrupted = true
	return true
}
