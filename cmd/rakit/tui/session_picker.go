package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Haslab-dev/RakitKode/agent"
)

const sessionPickerMaxRows = 12

type sessionEntry struct {
	id       string
	title    string
	updated  time.Time
	messages int
	tokensIn int
	tokensOt int
	state    agent.SessionState
}

type sessionPicker struct {
	open  bool
	items []sessionEntry
	view  []sessionEntry
	sel   int
	query string
}

// openSessionPicker lists sessions for the current workspace, newest first.
func (m *Model) openSessionPicker() {
	if m.mgr == nil {
		m.appendBlock(errorStyle.Render("no engine attached"))
		return
	}
	var sessions []*agent.Session
	if m.eng != nil {
		sessions = m.mgr.ListSessionsForFolder(m.eng.Workspace())
	}
	if len(sessions) == 0 {
		sessions = m.mgr.ListSessions()
	}
	entries := make([]sessionEntry, 0, len(sessions))
	for _, s := range sessions {
		if s.ParentSessionID != "" {
			continue // sub-agent transcripts are not resumable sessions
		}
		title := strings.TrimSpace(s.Name)
		if title == "" {
			for _, msg := range s.Messages {
				if msg.Role == "user" {
					title = oneLine(msg.Text())
					break
				}
			}
		}
		if title == "" {
			title = "(untitled)"
		}
		entries = append(entries, sessionEntry{
			id:       s.ID,
			title:    title,
			updated:  s.UpdatedAt,
			messages: len(s.Messages),
			tokensIn: s.TokenUsage.PromptTokens,
			tokensOt: s.TokenUsage.CompletionTokens,
			state:    s.State,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].updated.Equal(entries[j].updated) {
			return entries[i].updated.After(entries[j].updated)
		}
		return entries[i].id < entries[j].id
	})
	m.sessPicker = sessionPicker{open: true, items: entries}
	m.sessPicker.filter("")
	m.menuOpen = false
	m.pickerOpen = false
	m.atOpen = false
	m.textarea.Reset()
	m.textarea.Focus()
}

func (p *sessionPicker) filter(query string) {
	p.query = query
	q := strings.ToLower(strings.TrimSpace(query))
	p.view = p.view[:0]
	for _, e := range p.items {
		if q == "" || strings.Contains(strings.ToLower(e.title), q) || strings.Contains(strings.ToLower(e.id), q) {
			p.view = append(p.view, e)
		}
	}
	if p.sel >= len(p.view) {
		p.sel = len(p.view) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
}

func (p *sessionPicker) move(delta int) {
	p.sel += delta
	if p.sel < 0 {
		p.sel = 0
	}
	if p.sel >= len(p.view) {
		p.sel = len(p.view) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
}

// handleSessionPickerKey processes keys while the picker is open.
func (m *Model) handleSessionPickerKey(key string) {
	p := &m.sessPicker
	switch key {
	case "up", "ctrl+p":
		p.move(-1)
	case "down", "ctrl+n":
		p.move(1)
	case "esc":
		p.open = false
		m.textarea.Focus()
	case "enter":
		if p.sel >= 0 && p.sel < len(p.view) {
			m.switchSession(p.view[p.sel].id)
		}
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.filter(string(r[:len(r)-1]))
		}
	default:
		if r := []rune(key); len(r) == 1 {
			c := r[0]
			if c == ' ' || c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
				p.filter(p.query + key)
			}
		}
	}
}

// switchSession re-binds the TUI to another session and rebuilds its view.
func (m *Model) switchSession(id string) {
	if id == "" {
		return
	}
	m.closeStream()
	m.sessionID = id
	m.transcript = nil
	m.toolLines = map[string]int{}
	m.cur = streamBlocks{reasoning: -1, text: -1}
	m.streamOpen = false
	m.pendingApproval = nil
	m.pendingAsk = nil
	m.blockCursor = -1
	m.blockCursorActive = false
	m.sessPicker.open = false
	m.status = "ready"
	m.busy = false
	m.turnStart = time.Time{}
	m.textarea.Reset()
	m.pullSession()
	if sess, ok := m.mgr.GetSession(id); ok {
		m.busy = sess.State == agent.StateThinking || sess.State == agent.StateExecuting
	}
	m.appendSessionHeader()
	m.hydrateTranscript()
	m.textarea.Focus()
}

// renderSessionPicker renders the /resume history list.
func (m Model) renderSessionPicker() string {
	p := m.sessPicker
	if !p.open {
		return ""
	}
	if len(p.view) == 0 {
		return menuStyle.Render(statusStyle.Render("  no sessions match — type to filter · esc back"))
	}
	start := p.sel - sessionPickerMaxRows/2
	if start < 0 {
		start = 0
	}
	end := start + sessionPickerMaxRows
	if end > len(p.view) {
		end = len(p.view)
		start = end - sessionPickerMaxRows
		if start < 0 {
			start = 0
		}
	}
	rows := []string{headerStyle.Render(fmt.Sprintf("resume session  %d/%d", p.sel+1, len(p.view)))}
	for i := start; i < end; i++ {
		e := p.view[i]
		cursor, style := "  ", menuRowStyle
		if i == p.sel {
			cursor, style = "❯ ", menuRowSelectedStyle
		}
		mark := " "
		if e.id == m.sessionID {
			mark = "●"
		}
		when := e.updated.Local().Format("2006-01-02 15:04")
		if e.updated.IsZero() {
			when = "unknown"
		}
		line := fmt.Sprintf("%s%s %s  %-10s  %3d msgs  ↑%s ↓%s  %s",
			cursor, mark, when, e.state, e.messages,
			formatTokens(int64(e.tokensIn)), formatTokens(int64(e.tokensOt)),
			truncate(e.title, 44))
		rows = append(rows, style.Render(line))
	}
	hint := "↑/↓ move · type filter · enter open · esc back"
	if p.query != "" {
		hint = fmt.Sprintf("filter %q · %s", p.query, hint)
	}
	rows = append(rows, statusStyle.Render("  "+hint))
	return menuStyle.Render(strings.Join(rows, "\n"))
}
