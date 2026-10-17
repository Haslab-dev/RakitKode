package tui

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Haslab-dev/RakitKode/llm"
)

// command is one slash command.
type command struct {
	name        string
	description string
	// action runs when the command executes. It returns transcript lines and
	// an optional async command (long work like /mcp reconnect or /compact
	// runs in a goroutine and reports back via noticeMsg).
	action func(m *Model) ([]string, tea.Cmd)
}

// slashCommands is the registry shown by the "/" menu.
var slashCommands = []command{
	{
		name:        "/clear",
		description: "clear the screen (history is kept)",
		action:      actionClear,
	},
	{
		name:        "/compact",
		description: "summarize this session's history to free context",
		action:      actionCompact,
	},
	{
		name:        "/cost",
		description: "token metrics and estimated cost ($)",
		action:      actionCost,
	},
	{
		name:        "/diff",
		description: "view uncommitted workspace changes",
		action:      actionDiff,
	},
	{
		name:        "/effort",
		description: "reasoning effort: /effort low|medium|high|off",
		action:      actionEffort,
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
		name:        "/mcp",
		description: "MCP servers: /mcp [on|off|reconnect] [name]",
		action:      actionMCP,
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
		name:        "/skills",
		description: "list skills; /skills <name> shows details",
		action:      actionSkills,
	},
	{
		name:        "/status",
		description: "session status: model, tokens, tools, mcp, skills",
		action:      actionStatus,
	},
	{
		name:        "/tools",
		description: "list registered tools (core + MCP)",
		action:      actionTools,
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

// handleSlashInput interprets a "/..." submission. It returns transcript
// lines plus an optional async command whose noticeMsg lands later.
func (m *Model) handleSlashInput(value string) ([]string, tea.Cmd) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil, nil
	}
	// /model takes an argument before the generic registry dispatch.
	if fields[0] == "/model" {
		if len(fields) > 1 {
			return m.resolveModelSwitch(fields[1]), nil
		}
		m.openModelPicker("")
		m.pickerOpen = true
		return nil, nil
	}
	for _, c := range slashCommands {
		if fields[0] == c.name {
			m.pendingSlashArgs = fields[1:]
			var lines []string
			var cmd tea.Cmd
			if c.action == nil {
				lines, cmd = actionHelp(m)
			} else {
				lines, cmd = c.action(m)
			}
			m.pendingSlashArgs = nil
			return lines, cmd
		}
	}
	var names []string
	for _, c := range slashCommands {
		names = append(names, c.name)
	}
	return []string{errorStyle.Render("unknown command " + fields[0] + " — available: " + strings.Join(names, " "))}, nil
}

// resolveModelSwitch applies a typed "/model provider/model" argument to the
// current session (and the engine default).
func (m *Model) resolveModelSwitch(arg string) []string {
	return m.applyModelSwitch(arg)
}

// actionHelp lists the command registry.
func actionHelp(m *Model) ([]string, tea.Cmd) {
	out := []string{headerStyle.Render("commands")}
	for _, c := range slashCommands {
		out = append(out, fmt.Sprintf("  %s  %s", padRight(c.name, 12), statusStyle.Render(c.description)))
	}
	return out, nil
}

// actionToggleYolo flips the session's auto-approval flag.
func actionToggleYolo(m *Model) ([]string, tea.Cmd) {
	if m.mgr == nil {
		return []string{errorStyle.Render("no engine attached")}, nil
	}
	sess, ok := m.mgr.GetSession(m.sessionID)
	if !ok {
		return []string{errorStyle.Render("session not found")}, nil
	}
	next := !sess.AutoApprove
	if err := m.mgr.SetAutoApprove(m.sessionID, next); err != nil {
		return []string{errorStyle.Render("failed: " + err.Error())}, nil
	}
	if next {
		return []string{toolDotOKStyle.Render("✓ yolo on — mutating tool calls run without asking")}, nil
	}
	return []string{statusStyle.Render("yolo off — mutating tool calls ask for approval")}, nil
}

// actionShowSessions lists the manager's sessions, marking the current one.
func actionShowSessions(m *Model) ([]string, tea.Cmd) {
	out := []string{headerStyle.Render("sessions")}
	if m.mgr == nil {
		return append(out, statusStyle.Render("  no engine attached")), nil
	}
	sessions := m.mgr.ListSessions()
	if len(sessions) == 0 {
		return append(out, statusStyle.Render("  none saved")), nil
	}
	for _, s := range sessions {
		marker := " "
		if s.ID == m.sessionID {
			marker = "●"
		}
		id := s.ID
		if len(id) > 8 {
			id = id[:8]
		}
		out = append(out, fmt.Sprintf(" %s [%s] %-24s %-10s %d msgs",
			marker, id, truncate(s.Name, 24), s.State, len(s.Messages)))
	}
	return out, nil
}

// actionStatus renders a one-glance session panel: model, usage, tools,
// MCP servers, skills, approvals.
func actionStatus(m *Model) ([]string, tea.Cmd) {
	out := []string{headerStyle.Render("status")}
	if m.mgr == nil {
		return append(out, statusStyle.Render("  no engine attached")), nil
	}
	if sess, ok := m.mgr.GetSession(m.sessionID); ok {
		id := sess.ID
		if len(id) > 8 {
			id = id[:8]
		}
		out = append(out, fmt.Sprintf("  %-9s %s · %s", "session", id, sess.State))
	}
	out = append(out, fmt.Sprintf("  %-9s %s", "model", m.modelLabel()))
	if m.effortSet != "" {
		out = append(out, fmt.Sprintf("  %-9s %s", "effort", m.effortSet))
	}
	usage := fmt.Sprintf("↑ %s  ↓ %s", formatTokens(int64(m.tokensIn)), formatTokens(int64(m.tokensOut)))
	if m.contextTokens > 0 {
		usage += fmt.Sprintf(" · ctx %s", formatTokens(int64(m.contextTokens)))
	}
	if m.cacheHit > 0 {
		usage += fmt.Sprintf(" · cache %.0f%%", m.cacheHit*100)
	}
	out = append(out, fmt.Sprintf("  %-9s %s", "tokens", usage))
	cost := llm.EstimateCost(m.modelName, m.tokensIn, m.tokensOut)
	out = append(out, fmt.Sprintf("  %-9s %s", "cost", llm.FormatCost(cost)))
	if m.eng != nil {
		out = append(out, fmt.Sprintf("  %-9s %d core + %d mcp", "tools", len(coreToolNames(m)), m.mcpTools))
		out = append(out, fmt.Sprintf("  %-9s %d/%d servers connected · %d skills", "env", m.mcpUp, m.mcpTotal, m.skillCnt))
	}
	yolo := "off"
	if sess, ok := m.mgr.GetSession(m.sessionID); ok && sess.AutoApprove {
		yolo = "on"
	}
	out = append(out, fmt.Sprintf("  %-9s %s", "yolo", yolo))
	return out, nil
}

// actionCost renders session token metrics and estimated cost breakdown.
func actionCost(m *Model) ([]string, tea.Cmd) {
	out := []string{headerStyle.Render("session cost & tokens")}
	out = append(out, fmt.Sprintf("  %-12s %s", "model", toolStyle.Render(m.modelLabel())))
	out = append(out, fmt.Sprintf("  %-12s %s tokens", "prompt", formatTokens(int64(m.tokensIn))))
	out = append(out, fmt.Sprintf("  %-12s %s tokens", "completion", formatTokens(int64(m.tokensOut))))
	if m.contextTokens > 0 {
		out = append(out, fmt.Sprintf("  %-12s %s tokens (last turn)", "context", formatTokens(int64(m.contextTokens))))
	}
	if m.cacheHit > 0 {
		out = append(out, fmt.Sprintf("  %-12s %.1f%%", "cache hit", m.cacheHit*100))
	}
	cost := llm.EstimateCost(m.modelName, m.tokensIn, m.tokensOut)
	out = append(out, fmt.Sprintf("  %-12s %s", "est cost", brandStyle.Render(llm.FormatCost(cost))))
	return out, nil
}

// actionDiff shows uncommitted git modifications in the active workspace.
func actionDiff(m *Model) ([]string, tea.Cmd) {
	if m.eng == nil || m.eng.Workspace() == "" {
		return []string{statusStyle.Render("no workspace attached")}, nil
	}
	ws := m.eng.Workspace()
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = ws
	output, err := cmd.Output()
	if err != nil {
		return []string{errorStyle.Render("git status: " + err.Error())}, nil
	}
	outText := strings.TrimSpace(string(output))
	if outText == "" {
		return []string{toolDotOKStyle.Render("✓ working tree clean — no uncommitted changes")}, nil
	}
	lines := []string{headerStyle.Render("workspace changes (" + m.workspaceLabel() + "):")}
	for _, l := range strings.Split(outText, "\n") {
		lines = append(lines, "  "+l)
	}
	return lines, nil
}

// actionTools lists the registered tools: core set first, then MCP tools
// grouped by server.
func actionTools(m *Model) ([]string, tea.Cmd) {
	if m.eng == nil {
		return []string{headerStyle.Render("tools"), statusStyle.Render("  no engine attached")}, nil
	}
	core := coreToolNames(m)

	byServer := map[string][]string{}
	var servers []string
	for _, t := range m.eng.MCP().ListConnectedTools() {
		if _, seen := byServer[t.ServerName]; !seen {
			servers = append(servers, t.ServerName)
		}
		byServer[t.ServerName] = append(byServer[t.ServerName], t.Name)
	}
	sort.Strings(servers)
	total := 0
	for _, s := range servers {
		total += len(byServer[s])
	}

	out := []string{headerStyle.Render("tools")}
	if len(servers) == 0 {
		out = append(out, fmt.Sprintf("  core (%d)", len(core)))
	} else {
		out = append(out, fmt.Sprintf("  core (%d) · mcp (%d across %d servers)", len(core), total, len(servers)))
	}
	out = append(out, statusStyle.Render("    "+wrapNames(core, m.width, 4)))

	for _, s := range servers {
		out = append(out, fmt.Sprintf("  %s (%d)", mcpStyle.Render(s), len(byServer[s])))
		out = append(out, statusStyle.Render("    "+wrapNames(byServer[s], m.width, 4)))
	}
	return out, nil
}

// coreToolNames lists non-MCP tool names from the registry, sorted.
func coreToolNames(m *Model) []string {
	var names []string
	for _, def := range m.eng.Tools().Definitions() {
		if strings.HasPrefix(def.Function.Name, "mcp__") {
			continue
		}
		names = append(names, def.Function.Name)
	}
	sort.Strings(names)
	return names
}

// wrapNames renders names joined by " · ", hard-wrapped at the given width
// with indent spaces on continuation lines.
func wrapNames(names []string, width, indentSpaces int) string {
	if len(names) == 0 {
		return "(none)"
	}
	var b strings.Builder
	col := 0
	for i, n := range names {
		item := n
		if i > 0 {
			item = " · " + n
		}
		if col > 0 && col+len([]rune(item)) > width-8 {
			b.WriteString("\n" + strings.Repeat(" ", indentSpaces))
			item = n
			col = 0
		}
		b.WriteString(item)
		col += len([]rune(item))
	}
	return b.String()
}

// actionSkills lists discovered skills, or shows one skill's details
// (/skills <name>).
func actionSkills(m *Model) ([]string, tea.Cmd) {
	arg := ""
	if args := m.slashArgs(); len(args) > 0 {
		arg = args[0]
	}
	return skillLines(m, arg), nil
}

// skillLines renders the /skills listing or one skill's detail. arg selects
// a single skill by name.
func skillLines(m *Model, arg string) []string {
	out := []string{headerStyle.Render("skills")}
	if m.eng == nil {
		return append(out, statusStyle.Render("  no engine attached"))
	}
	list := m.eng.Skills()
	if len(list) == 0 {
		return append(out, statusStyle.Render("  none discovered (workspace .claude/skills, ~/.rakit/skills, plugins)"))
	}
	if arg != "" {
		for _, s := range list {
			if s.Name == arg {
				out = append(out,
					fmt.Sprintf("  %s  (%s)", toolStyle.Render(s.Name), s.Scope),
					statusStyle.Render("    path  "+s.Path),
				)
				if s.Description != "" {
					out = append(out, statusStyle.Render("    desc  "+truncate(oneLine(s.Description), 100)))
				}
				if len(s.Scripts) > 0 {
					out = append(out, statusStyle.Render("    scripts  "+strings.Join(s.Scripts, ", ")))
				}
				return out
			}
		}
		return append(out, errorStyle.Render("skill not found: "+arg))
	}
	// Adaptive name column: wide enough for the longest name (capped).
	nameW := 0
	for _, s := range list {
		if l := len([]rune(s.Name)); l > nameW && l <= 28 {
			nameW = l
		}
	}
	// Keep the whole row inside the transcript width so soft wrap never
	// leaves a one-character sliver row below the fold.
	descBudget := m.width - nameW - 10
	if descBudget < 16 {
		descBudget = 16
	}
	for _, s := range list {
		out = append(out, fmt.Sprintf("  ● %s  %s",
			toolStyle.Render(padRight(s.Name, nameW)), statusStyle.Render(truncate(oneLine(s.Description), descBudget))))
	}
	return out
}

// actionMCP renders the MCP server table and applies subcommands:
// /mcp on|off <name>, /mcp reconnect.
func actionMCP(m *Model) ([]string, tea.Cmd) {
	if m.eng == nil {
		return []string{headerStyle.Render("mcp"), statusStyle.Render("  no engine attached")}, nil
	}
	args := m.slashArgs()
	switch {
	case len(args) >= 1 && args[0] == "reconnect":
		return []string{statusStyle.Render("reconnecting mcp servers…")}, m.mcpReconnectCmd()
	case len(args) >= 2 && (args[0] == "on" || args[0] == "off"):
		return m.mcpToggle(args[0] == "on", args[1])
	}
	return m.mcpTable(), nil
}

// slashArgs returns the argument words of the slash input that triggered the
// current action (the input is reset by the time actions run, so callers
// pass it through handleSlashInput; the picker path is unaffected).
func (m *Model) slashArgs() []string {
	return m.pendingSlashArgs
}

// mcpTable renders server rows with status dots, type, and tool counts.
func (m *Model) mcpTable() []string {
	out := []string{headerStyle.Render("mcp servers")}
	servers := m.eng.MCPStatus()
	if len(servers) == 0 {
		return append(out,
			statusStyle.Render("  none configured"),
			statusStyle.Render("  add one under "+m.eng.Home()+"/mcp_servers.json, then /mcp reconnect"),
		)
	}
	for _, s := range servers {
		mark, style := "●", toolDotOKStyle
		detail := fmt.Sprintf("%d tools", s.ToolCount)
		switch {
		case s.Status == "error":
			mark, style = "✗", toolDotErrStyle
			detail = oneLine(truncate(s.LastError, 60))
		case s.Status != "connected":
			mark, style = "○", statusStyle
			detail = "disabled"
		}
		target := s.Command
		if s.Type == "remote" || s.URL != "" {
			target = s.URL
		}
		out = append(out, fmt.Sprintf("  %s %-18s %s",
			style.Render(mark), toolStyle.Render(s.Name), statusStyle.Render(detail)))
		if target != "" && s.Status != "error" {
			out = append(out, statusStyle.Render("    "+truncate(target, m.width-8)))
		}
	}
	out = append(out, statusStyle.Render("  /mcp reconnect · /mcp on|off <name>"))
	return out
}

// mcpToggle enables or disables one server and reconnects in the background.
func (m *Model) mcpToggle(enable bool, name string) ([]string, tea.Cmd) {
	for _, s := range m.eng.MCPStatus() {
		if s.Name != name {
			continue
		}
		if s.Enabled == enable {
			state := "disabled"
			if enable {
				state = "enabled"
			}
			return []string{statusStyle.Render(fmt.Sprintf("%s is already %s", name, state))}, nil
		}
		s.Enabled = enable
		// Don't persist a stale connection status across the toggle; the
		// reconnect below refreshes it for enabled servers.
		s.Status = ""
		if !enable {
			s.Status = "disabled"
		}
		s.ToolCount = 0
		if _, err := m.eng.MCP().SaveServer(s); err != nil {
			return []string{errorStyle.Render("failed: " + err.Error())}, nil
		}
		verb := "disabled"
		if enable {
			verb = "enabled"
		}
		return []string{statusStyle.Render(name + " " + verb + " — reconnecting…")}, m.mcpReconnectCmd()
	}
	return []string{errorStyle.Render("no mcp server named " + name)}, nil
}

// mcpReconnectCmd reconnects every configured server off the UI goroutine
// and reports the resulting table via noticeMsg.
func (m *Model) mcpReconnectCmd() tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		eng.MCP().DisconnectAll()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := eng.MCP().ConnectAll(ctx); err != nil {
			return noticeMsg{lines: []string{errorStyle.Render("mcp reconnect: " + err.Error())}}
		}
		eng.RegisterMCPTools()
		var lines []string
		up := 0
		servers := eng.MCPStatus()
		for _, s := range servers {
			switch s.Status {
			case "connected":
				up++
				lines = append(lines, toolDotOKStyle.Render(fmt.Sprintf("  ● %s — %d tools", s.Name, s.ToolCount)))
			case "error":
				lines = append(lines, toolDotErrStyle.Render("  ✗ "+s.Name+" — "+oneLine(truncate(s.LastError, 64))))
			default:
				lines = append(lines, statusStyle.Render("  ○ "+s.Name+" — disabled"))
			}
		}
		head := fmt.Sprintf("mcp reconnect: %d/%d servers connected", up, len(servers))
		return noticeMsg{lines: append([]string{headerStyle.Render(head)}, lines...)}
	}
}

// actionCompact compacts the session history in the background.
func actionCompact(m *Model) ([]string, tea.Cmd) {
	if m.mgr == nil {
		return []string{errorStyle.Render("no engine attached")}, nil
	}
	if m.busy {
		return []string{errorStyle.Render("finish the current turn first")}, nil
	}
	eng, sid := m.eng, m.sessionID
	return []string{statusStyle.Render("compacting session history…")}, func() tea.Msg {
		if err := eng.Compact(sid); err != nil {
			return noticeMsg{lines: []string{errorStyle.Render("compact failed: " + err.Error())}}
		}
		return noticeMsg{lines: []string{toolDotOKStyle.Render("✓ history compacted — context freed")}}
	}
}

// actionEffort shows or sets the reasoning-effort hint.
func actionEffort(m *Model) ([]string, tea.Cmd) {
	args := m.slashArgs()
	// Validate before the engine guard so typos surface regardless.
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "low", "medium", "high", "off", "none":
		default:
			return []string{errorStyle.Render("unknown effort " + args[0] + " — use low|medium|high|off")}, nil
		}
	}
	if m.eng == nil {
		return []string{errorStyle.Render("no engine attached")}, nil
	}
	if len(args) == 0 {
		if m.effortSet == "" {
			return []string{statusStyle.Render("effort: engine default — set with /effort low|medium|high|off")}, nil
		}
		return []string{statusStyle.Render("effort: " + m.effortSet)}, nil
	}
	value := strings.ToLower(args[0])
	switch value {
	case "low", "medium", "high":
	case "off", "none":
		value = "off"
	}
	m.eng.ReasoningEffort(value)
	if value == "off" {
		m.effortSet = ""
		return []string{statusStyle.Render("✓ reasoning effort off (provider default)")}, nil
	}
	m.effortSet = value
	return []string{toolDotOKStyle.Render("✓ reasoning effort → " + value)}, nil
}

// actionClear resets the transcript; session history on disk is kept.
func actionClear(m *Model) ([]string, tea.Cmd) {
	m.transcript = nil
	m.toolLines = map[string]int{}
	m.cur = streamBlocks{reasoning: -1, text: -1}
	m.streamOpen = false
	m.appendSessionHeader()
	return nil, nil
}

// actionQuit requests program exit.
func actionQuit(m *Model) ([]string, tea.Cmd) {
	m.quitRequested = true
	return nil, nil
}

// menuMaxRows caps the slash menu height; the list windows around the
// selection so a long registry stays inside the footer.
const menuMaxRows = 8

// renderSlashMenu builds the popup rows above the input: a windowed list
// around the selection, plus the navigation hint.
func (m Model) renderSlashMenu() string {
	if !m.menuOpen || len(m.menuItems) == 0 {
		return ""
	}
	start := m.menuSelected - menuMaxRows/2
	if start < 0 {
		start = 0
	}
	end := start + menuMaxRows
	if end > len(m.menuItems) {
		end = len(m.menuItems)
		start = end - menuMaxRows
		if start < 0 {
			start = 0
		}
	}

	var rows []string
	for i := start; i < end; i++ {
		item := m.menuItems[i]
		cursor, style := "  ", menuRowStyle
		if i == m.menuSelected {
			cursor, style = "❯ ", menuRowSelectedStyle
		}
		rows = append(rows, style.Render(cursor+padRight(item.text, 11)+item.desc))
	}
	hint := fmt.Sprintf("  ↑/↓ navigate · enter select · tab complete · esc close  %d/%d",
		m.menuSelected+1, len(m.menuItems))
	rows = append(rows, statusStyle.Render(hint))
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
