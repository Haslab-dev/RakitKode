package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/Haslab-dev/RakitKode/internal/agent/tools/mcp"
	"github.com/Haslab-dev/RakitKode/internal/skills"
)

// command is one slash command.
type command struct {
	name        string
	description string
	// action runs when selected. Returns lines to append.
	action func(m *Model) []string
}

// slashCommands is the registry shown by the "/" menu.
var slashCommands = []command{
	{
		name:        "/model",
		description: "switch the active model",
		action:      actionPickModel,
	},
	{
		name:        "/mcp",
		description: "show MCP servers and their tools",
		action:      actionShowMCP,
	},
	{
		name:        "/skills",
		description: "list discovered skills",
		action:      actionShowSkills,
	},
	{
		name:        "/tools",
		description: "list the active tool palette",
		action:      actionShowTools,
	},
	{
		name:        "/context",
		description: "show context files in the system prompt",
		action:      actionShowContext,
	},
	{
		name:        "/clear",
		description: "clear the screen (history is kept)",
		action: func(m *Model) []string {
			m.transcript = nil
			m.toolLines = map[string]int{}
			m.appendSessionHeader()
			return nil
		},
	},
	{
		name:        "/quit",
		description: "exit RakitKode",
		action: func(m *Model) []string {
			m.quitRequested = true
			return nil
		},
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

// filterCommands fuzzy-matches the query against the registry.
func (m *Model) filterCommands(query string) []slashItem {
	items := make([]slashItem, 0, len(slashCommands))
	for i, c := range slashCommands {
		items = append(items, slashItem{text: c.name, desc: c.description, index: i})
	}
	if query == "" {
		return items
	}
	var targets []string
	for _, it := range items {
		targets = append(targets, strings.TrimPrefix(it.text, "/"))
	}
	matches := fuzzy.Find(query, targets)
	out := make([]slashItem, 0, len(matches))
	for _, match := range matches {
		out = append(out, items[match.Index])
	}
	return out
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

// executeMenuSelection runs the highlighted command and closes the menu.
func (m *Model) executeMenuSelection() []string {
	if !m.menuOpen || m.menuSelected >= len(m.menuItems) {
		return nil
	}
	item := m.menuItems[m.menuSelected]
	m.menuOpen = false
	return slashCommands[item.index].action(m)
}

// actionPickModel renders the provider/model catalog as selectable rows.
func actionPickModel(m *Model) []string {
	cfg := m.app.Config.Config()
	var out []string
	out = append(out, headerStyle.Render("models — use /model <provider/model> to switch"))
	ids := make([]string, 0)
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	current := m.app.Coordinator.Agent().CurrentModelName()
	for _, id := range ids {
		p := cfg.Providers[id]
		for _, model := range p.Models {
			marker := " "
			if id+"/"+model.ID == current {
				marker = "•"
			}
			out = append(out, fmt.Sprintf(" %s %s/%s  (%s, ctx %dk)",
				marker, id, model.ID, p.Name, model.ContextWindow/1000))
		}
	}
	out = append(out, statusStyle.Render("switch with: /model myairouter/mimo/mimo-v2.6-flash"))
	return out
}

// actionShowMCP lists servers, connection state, and tools.
func actionShowMCP(m *Model) []string {
	cfg := m.app.Config.Config()
	var out []string
	out = append(out, headerStyle.Render("mcp servers"))
	if len(cfg.MCP) == 0 {
		return append(out, statusStyle.Render("  none configured (add under \"mcp\" in rakitkode.json)"))
	}
	for _, st := range mcp.GetStates() {
		icon := map[mcp.State]string{
			mcp.StateConnected: "●",
			mcp.StateFailed:    "✗",
			mcp.StateConnecting: "◌",
			mcp.StateNotStarted: "○",
		}[st.State]
		line := fmt.Sprintf("  %s %s — %s", icon, st.Name, st.State)
		if st.Err != "" {
			line += " (" + st.Err + ")"
		}
		out = append(out, line)
		if tools, ok := mcp.GetTools(st.Name); ok {
			for _, t := range tools {
				out = append(out, toolStyle.Render(fmt.Sprintf("      ↳ %s", t.Name)))
			}
		}
	}
	return out
}

// actionShowSkills lists every discovered skill.
func actionShowSkills(m *Model) []string {
	var out []string
	out = append(out, headerStyle.Render("skills"))
	found := m.discoveredSkills()
	if len(found) == 0 {
		return append(out, statusStyle.Render("  none found (add SKILL.md under .rakitkode/skills/)"))
	}
	loaded := m.app.Coordinator.Agent().LoadedSkillNames()
	for _, s := range found {
		marker := " "
		for _, name := range loaded {
			if name == s.Name {
				marker = "✓"
			}
		}
		out = append(out, fmt.Sprintf(" %s %s — %s", marker, s.Name, truncate(s.Description, 80)))
		out = append(out, statusStyle.Render("      "+s.SkillFilePath))
	}
	return out
}

// actionShowTools lists the active tool palette with counts.
func actionShowTools(m *Model) []string {
	var out []string
	tools := m.app.Coordinator.Agent().ToolNames()
	out = append(out, headerStyle.Render(fmt.Sprintf("tools (%d active)", len(tools))))
	for _, name := range tools {
		if strings.HasPrefix(name, "mcp_") {
			out = append(out, fmt.Sprintf("  ⚙ %s  %s", name, statusStyle.Render("(mcp)")))
			continue
		}
		out = append(out, fmt.Sprintf("  ⚙ %s", name))
	}
	return out
}

// actionShowContext lists context files injected into the system prompt.
func actionShowContext(m *Model) []string {
	var out []string
	out = append(out, headerStyle.Render("context files"))
	paths := []string{"AGENTS.md", "RAKITKODE.md", "CLAUDE.md", "GEMINI.md", ".cursorrules"}
	cfg := m.app.Config.Config()
	if cfg.Options != nil && len(cfg.Options.ContextPaths) > 0 {
		paths = cfg.Options.ContextPaths
	}
	found := 0
	for _, p := range paths {
		if fileExists(p) {
			out = append(out, fmt.Sprintf("  • %s", p))
			found++
		}
	}
	skillsPaths := []string{".rakitkode/skills"}
	if cfg.Options != nil && len(cfg.Options.SkillsPaths) > 0 {
		skillsPaths = cfg.Options.SkillsPaths
	}
	out = append(out, statusStyle.Render("  skills paths: "+strings.Join(skillsPaths, ", ")))
	if found == 0 {
		out = append(out, statusStyle.Render("  no context files found"))
	}
	return out
}

// resolveModelSwitch parses "provider/model..." and switches the agent.
func (m *Model) resolveModelSwitch(arg string) []string {
	idx := strings.Index(arg, "/")
	if idx <= 0 {
		return []string{errorStyle.Render(fmt.Sprintf("model must be provider/model, got %q", arg))}
	}
	provider, name := arg[:idx], arg[idx+1:]
	cfg := m.app.Config.Config()
	if _, ok := cfg.Providers[provider]; !ok {
		return []string{errorStyle.Render(fmt.Sprintf("provider %q not configured", provider))}
	}
	found := false
	for _, model := range cfg.Providers[provider].Models {
		if model.ID == name {
			found = true
			break
		}
	}
	if !found {
		return []string{errorStyle.Render(fmt.Sprintf("model %q not in provider %q catalog", name, provider))}
	}
	if err := m.app.Coordinator.SwitchModel(context.Background(), provider, name); err != nil {
		return []string{errorStyle.Render("switch failed: " + err.Error())}
	}
	m.appendSessionHeader()
	return []string{headerStyle.Render(fmt.Sprintf("switched to %s/%s", provider, name))}
}

// discoveredSkills loads skills from the configured paths.
func (m *Model) discoveredSkills() []*skills.Skill {
	cfg := m.app.Config.Config()
	paths := []string{".rakitkode/skills"}
	if cfg.Options != nil && len(cfg.Options.SkillsPaths) > 0 {
		paths = cfg.Options.SkillsPaths
	}
	wd := m.app.Config.WorkingDir()
	abs := make([]string, 0, len(paths))
	for _, p := range paths {
		abs = append(abs, joinPath(wd, p))
	}
	return skills.Discover(abs)
}

// handleSlashInput interprets a "/..." submission.
func (m *Model) handleSlashInput(value string) []string {
	fields := strings.Fields(value)
	switch fields[0] {
	case "/model":
		if len(fields) < 2 {
			return actionPickModel(m)
		}
		return m.resolveModelSwitch(fields[1])
	case "/mcp":
		return actionShowMCP(m)
	case "/skills":
		return actionShowSkills(m)
	case "/tools":
		return actionShowTools(m)
	case "/context":
		return actionShowContext(m)
	case "/clear":
		m.transcript = nil
		m.toolLines = map[string]int{}
		m.appendSessionHeader()
		return nil
	case "/quit":
		m.quitRequested = true
		return nil
	default:
		var names []string
		for _, c := range slashCommands {
			names = append(names, c.name)
		}
		return []string{errorStyle.Render("unknown command " + fields[0] + " — available: " + strings.Join(names, " "))}
	}
}

// renderSlashMenu builds the popup rows above the input.
func (m *Model) renderSlashMenu() string {
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
	rows = append(rows, statusStyle.Render("  ↑/↓ navigate • enter select • esc close"))
	return menuStyle.Render(strings.Join(rows, "\n"))
}

// sessionHeader is the banner shown after connect/model switch.
func (m *Model) appendSessionHeader() {
	agent := m.app.Coordinator.Agent()
	model := agent.CurrentModelName()
	tools := len(agent.ToolNames())
	skillsCount := len(m.discoveredSkills())
	mcpCount := len(mcp.GetStates())
	m.appendBlock(titleStyle.Render(" RakitKode ") +
		statusStyle.Render(fmt.Sprintf("  model %s · %d tools · %d skills · %d mcp servers",
			model, tools, skillsCount, mcpCount)))
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

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// joinPath joins a working dir with a possibly-relative path.
func joinPath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
