package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// command is one slash command.
type command struct {
	name        string
	description string
	// action runs when the command executes. Returns lines to append.
	action func(m *Model) []string
}

// slashCommands is the registry shown by the "/" menu.
var slashCommands = []command{
	{
		name:        "/clear",
		description: "clear the screen (history is kept)",
		action:      actionClear,
	},
	{
		name:        "/exit",
		description: "exit rakit",
		action:      actionQuit,
	},
	{
		name:        "/help",
		description: "list commands",
		// action is nil (listing the registry from its own initializer
		// would be an initialization cycle); handleSlashInput special-cases it.
		action: nil,
	},
	{
		name:        "/model",
		description: "pick the model for this session (interactive)",
		action:      nil, // special-cased in handleSlashInput: opens the picker
	},
	{
		name:        "/quit",
		description: "exit rakit",
		action:      actionQuit,
	},
	{
		name:        "/sessions",
		description: "list agent sessions",
		action:      actionShowSessions,
	},
	{
		name:        "/yolo",
		description: "toggle tool-call auto-approval",
		action:      actionToggleYolo,
	},
}

// slashItem is one row in the slash menu.
type slashItem struct {
	text  string
	desc  string
	index int
}

// openSlashMenu activates the "/" popup filtered by query.
func (m *Model) openSlashMenu(query string) {
	m.menuItems = m.filterCommands(query)
	m.menuSelected = 0
	m.menuOpen = len(m.menuItems) > 0
}

// filterCommands matches the query against the registry: prefix matches
// first, then substring matches.
func (m *Model) filterCommands(query string) []slashItem {
	items := make([]slashItem, 0, len(slashCommands))
	for i, c := range slashCommands {
		items = append(items, slashItem{text: c.name, desc: c.description, index: i})
	}
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var prefix, substring []slashItem
	for _, it := range items {
		name := strings.TrimPrefix(it.text, "/")
		switch {
		case strings.HasPrefix(name, q):
			prefix = append(prefix, it)
		case strings.Contains(name, q):
			substring = append(substring, it)
		}
	}
	return append(prefix, substring...)
}

// moveMenuSelection moves the highlight by delta, clamping.
func (m *Model) moveMenuSelection(delta int) {
	if !m.menuOpen || len(m.menuItems) == 0 {
		return
	}
	m.menuSelected += delta
	if m.menuSelected < 0 {
		m.menuSelected = 0
	}
	if m.menuSelected >= len(m.menuItems) {
		m.menuSelected = len(m.menuItems) - 1
	}
}

// menuEnter applies enter/tab on the open menu. Tab completes the highlighted
// command into the input; enter runs the command when the typed input already
// names one exactly, otherwise it selects the highlighted completion.
func (m *Model) menuEnter(key string) tea.Cmd {
	value := m.textarea.Value()
	m.textarea.Reset()
	if key == "tab" {
		if m.menuSelected < len(m.menuItems) {
			m.textarea.SetValue(m.menuItems[m.menuSelected].text + " ")
			m.menuOpen = false
		}
		return nil
	}
	typed := strings.TrimSpace(value)
	exact := false
	if fields := strings.Fields(typed); len(fields) > 0 {
		for _, c := range slashCommands {
			if fields[0] == c.name {
				exact = true
				break
			}
		}
	}
	if exact {
		m.textarea.Reset()
		m.menuOpen = false
		m.appendBlock(userStyle.Render("❯ ") + typed)
		return m.runSlashInput(typed)
	}
	if m.menuSelected < len(m.menuItems) {
		m.textarea.SetValue(m.menuItems[m.menuSelected].text + " ")
	}
	m.menuOpen = false
	return nil
}

// handleSlashInput interprets a "/..." submission.
func (m *Model) handleSlashInput(value string) []string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	// /model takes an argument before the generic registry dispatch.
	if fields[0] == "/model" {
		if len(fields) > 1 {
			return m.resolveModelSwitch(fields[1])
		}
		m.openModelPicker("")
		m.pickerOpen = true
		return nil
	}
	for _, c := range slashCommands {
		if fields[0] == c.name {
			if c.action == nil {
				return actionHelp(m)
			}
			return c.action(m)
		}
	}
	var names []string
	for _, c := range slashCommands {
		names = append(names, c.name)
	}
	return []string{errorStyle.Render("unknown command " + fields[0] + " — available: " + strings.Join(names, " "))}
}

// actionShowModel renders the provider/model catalog with the active model.
func actionShowModel(m *Model) []string {
	out := []string{headerStyle.Render("models")}
	if m.eng == nil {
		return append(out, statusStyle.Render("  no engine attached"))
	}
	providerID, model := m.eng.ModelInfo()
	out = append(out, fmt.Sprintf("  • active: %s/%s", providerID, model))
	if m.modelName != "" && m.modelName != model {
		out = append(out, statusStyle.Render("  this session pins: "+m.modelName))
	}
	for _, p := range m.eng.Profiles() {
		state := "enabled"
		if !p.Enabled {
			state = "disabled"
		}
		out = append(out, fmt.Sprintf("  %s %s (%s)", bullet(providerID, p.ID), p.ID, state))
		models := p.SelectedModels
		if len(models) == 0 {
			models = p.AvailableModels
		}
		for i, id := range models {
			if i == 12 {
				out = append(out, statusStyle.Render(fmt.Sprintf("      … %d more", len(models)-12)))
				break
			}
			out = append(out, statusStyle.Render("      · "+p.ID+"/"+id))
		}
	}
	out = append(out, statusStyle.Render("switch with: /model provider/model (new sessions; the current session keeps its model)"))
	return out
}

// resolveModelSwitch applies a typed "/model provider/model" argument to the
// current session (and the engine default).
func (m *Model) resolveModelSwitch(arg string) []string {
	return m.applyModelSwitch(arg)
}

// bullet marks the active provider in catalog listings.
func bullet(active, id string) string {
	if id == active {
		return "•"
	}
	return " "
}

// actionHelp lists the command registry.
func actionHelp(m *Model) []string {
	out := []string{headerStyle.Render("commands")}
	for _, c := range slashCommands {
		out = append(out, fmt.Sprintf("  %s  %s", padRight(c.name, 12), statusStyle.Render(c.description)))
	}
	return out
}

// actionToggleYolo flips the session's auto-approval flag.
func actionToggleYolo(m *Model) []string {
	if m.mgr == nil {
		return []string{errorStyle.Render("no engine attached")}
	}
	sess, ok := m.mgr.GetSession(m.sessionID)
	if !ok {
		return []string{errorStyle.Render("session not found")}
	}
	next := !sess.AutoApprove
	if err := m.mgr.SetAutoApprove(m.sessionID, next); err != nil {
		return []string{errorStyle.Render("failed: " + err.Error())}
	}
	if next {
		return []string{toolStyle.Render("yolo on — mutating tool calls run without asking")}
	}
	return []string{toolStyle.Render("yolo off — mutating tool calls ask for approval")}
}

// actionShowSessions lists the manager's sessions, marking the current one.
func actionShowSessions(m *Model) []string {
	out := []string{headerStyle.Render("sessions")}
	if m.mgr == nil {
		return append(out, statusStyle.Render("  no engine attached"))
	}
	sessions := m.mgr.ListSessions()
	if len(sessions) == 0 {
		return append(out, statusStyle.Render("  none saved"))
	}
	for _, s := range sessions {
		marker := " "
		if s.ID == m.sessionID {
			marker = "•"
		}
		id := s.ID
		if len(id) > 8 {
			id = id[:8]
		}
		out = append(out, fmt.Sprintf(" %s [%s] %-24s %-10s %d msgs",
			marker, id, truncate(s.Name, 24), s.State, len(s.Messages)))
	}
	return out
}

// actionClear resets the transcript; session history on disk is kept.
func actionClear(m *Model) []string {
	m.transcript = nil
	m.toolLines = map[string]int{}
	m.cur = streamBlocks{reasoning: -1, text: -1}
	m.streamOpen = false
	m.appendSessionHeader()
	return nil
}

// actionQuit requests program exit.
func actionQuit(m *Model) []string {
	m.quitRequested = true
	return nil
}

// renderSlashMenu builds the popup rows above the input.
func (m Model) renderSlashMenu() string {
	if !m.menuOpen {
		return ""
	}
	var rows []string
	for i, item := range m.menuItems {
		cursor := "  "
		style := menuRowStyle
		if i == m.menuSelected {
			cursor = "> "
			style = menuRowSelectedStyle
		}
		rows = append(rows, style.Render(cursor+padRight(item.text, 12)+statusStyle.Render(item.desc)))
	}
	rows = append(rows, statusStyle.Render("  ↑/↓ navigate · enter select · tab complete · esc close"))
	return menuStyle.Render(strings.Join(rows, "\n"))
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}
