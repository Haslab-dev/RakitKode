package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// historyPath is the persisted input history (<home>/history).
func (m *Model) historyPath() string {
	if m.eng == nil {
		return ""
	}
	return filepath.Join(m.eng.Home(), "history")
}

// loadHistory reads the most recent 200 entries.
func (m *Model) loadHistory() {
	path := m.historyPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			m.history = append(m.history, l)
		}
	}
	m.histIdx = len(m.history)
}

// recordHistory appends an entry and persists it, skipping consecutive
// duplicates.
func (m *Model) recordHistory(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(m.history) > 0 && m.history[len(m.history)-1] == value {
		m.histIdx = len(m.history)
		return
	}
	m.history = append(m.history, value)
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	m.histIdx = len(m.history)
	if path := m.historyPath(); path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(value + "\n")
			_ = f.Close()
		}
	}
}

// historyPrev recalls an older entry.
func (m *Model) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx >= len(m.history) {
		m.histDraft = m.textarea.Value()
		m.histIdx = len(m.history)
	}
	if m.histIdx > 0 {
		m.histIdx--
	}
	m.textarea.SetValue(m.history[m.histIdx])
}

// historyNext moves toward the newest entry, restoring the draft at the end.
func (m *Model) historyNext() {
	if len(m.history) == 0 || m.histIdx >= len(m.history) {
		return
	}
	m.histIdx++
	if m.histIdx >= len(m.history) {
		m.textarea.SetValue(m.histDraft)
		return
	}
	m.textarea.SetValue(m.history[m.histIdx])
}
