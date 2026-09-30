// Package tui implements the terminal UI.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Haslab-dev/RakitKode/internal/agent"
	"github.com/Haslab-dev/RakitKode/internal/app"
	"github.com/Haslab-dev/RakitKode/internal/message"
	"github.com/Haslab-dev/RakitKode/internal/permission"
)

var (
	titleStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	reasoningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("179"))

	toolErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("174"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238"))

	permBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(0, 1)

	menuStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	menuRowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	menuRowSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("62"))
)

// blockKind identifies a mutable streamed block in the transcript.
type blockKind int

const (
	blockText blockKind = iota
	blockReasoning
)

// block is one replaceable line in the transcript (streamed content
// mutates in place instead of appending every delta).
type block struct {
	kind    blockKind
	content string
	// reasoningOpen tracks collapsed state; reasoning starts collapsed.
	reasoningOpen bool
}

// Model is the Bubble Tea model of the chat UI.
type Model struct {
	app       *app.App
	sessionID string

	viewport viewport.Model
	textarea textarea.Model

	ready  bool
	width  int
	height int
	busy   bool
	status string

	// transcript is the rendered history; streamed blocks mutate in place.
	transcript []block
	// streamBlocks maps assistant message IDs to their mutable
	// reasoning/text block indexes (the message service republishes the
	// full message on every update, so deltas must reuse the same rows).
	streamBlocks map[string]*streamBlocks
	// toolLines maps tool call IDs to transcript indexes.
	toolLines map[string]int
	// toolResolved marks tool calls whose result already updated the line.
	toolResolved map[string]bool

	pending *permission.PermissionRequest

	// slash menu state
	menuOpen     bool
	menuItems    []slashItem
	menuSelected int

	// stats
	tokensIn  int64
	tokensOut int64
	ctxWindow int64

	quitRequested bool
}

// streamBlocks holds the transcript indexes of one assistant message's
// mutable rendering blocks (-1 when the part has not appeared yet).
type streamBlocks struct {
	reasoning int
	text      int
}

// New builds the TUI model.
func New(a *app.App, sessionID string) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message… / for commands"
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 8
	ta.Focus()

	vp := viewport.New()
	m := Model{
		app:          a,
		sessionID:    sessionID,
		textarea:     ta,
		viewport:     vp,
		status:       "ready",
		streamBlocks: map[string]*streamBlocks{},
		toolLines:    map[string]int{},
		toolResolved: map[string]bool{},
	}
	m.appendSessionHeader()
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return textarea.Blink }

// appendBlock adds an immutable line to the transcript.
func (m *Model) appendBlock(content string) {
	m.transcript = append(m.transcript, block{content: content})
	m.refreshViewport()
}

// streamBlocksFor returns (creating if needed) the mutable block set of
// an assistant message.
func (m *Model) streamBlocksFor(msgID string) *streamBlocks {
	sb, ok := m.streamBlocks[msgID]
	if !ok {
		sb = &streamBlocks{reasoning: -1, text: -1}
		m.streamBlocks[msgID] = sb
	}
	return sb
}

// ensureBlock appends a new block of the given kind and returns its index.
func (m *Model) ensureBlock(kind blockKind) int {
	m.transcript = append(m.transcript, block{kind: kind, reasoningOpen: kind == blockReasoning})
	return len(m.transcript) - 1
}

// refreshViewport re-renders all blocks and syncs the scroll area.
func (m *Model) refreshViewport() {
	var sb strings.Builder
	for i := range m.transcript {
		sb.WriteString(m.renderBlock(i))
		sb.WriteString("\n")
	}
	m.viewport.SetContent(strings.TrimSuffix(sb.String(), "\n"))
	m.viewport.GotoBottom()
}

// renderBlock renders one transcript entry.
func (m *Model) renderBlock(i int) string {
	b := &m.transcript[i]
	switch b.kind {
	case blockReasoning:
		label := "✻ thinking"
		if b.reasoningOpen {
			body := reasoningStyle.Render(indent(b.content, "│ "))
			return reasoningStyle.Render(label+" (tab to collapse)") + "\n" + body
		}
		preview := oneLine(b.content)
		if len([]rune(preview)) > 72 {
			preview = string([]rune(preview)[:72]) + "…"
		}
		if preview == "" {
			return reasoningStyle.Render(label + "…")
		}
		return reasoningStyle.Render(fmt.Sprintf("%s (tab to expand): %s", label, preview))
	default:
		return b.content
	}
}

// indent prefixes every line of s.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		if lines[i] != "" {
			lines[i] = prefix + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// oneLine flattens a string to its first non-empty line.
func oneLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.textarea.SetWidth(msg.Width - 4)
		m.viewport.SetWidth(msg.Width - 2)
		m.viewport.SetHeight(msg.Height - 12)
		m.refreshViewport()

	case tea.KeyPressMsg:
		key := msg.String()
		if m.menuOpen {
			switch key {
			case "up", "ctrl+p":
				m.moveMenuSelection(-1)
				return m, nil
			case "down", "ctrl+n":
				m.moveMenuSelection(1)
				return m, nil
			case "esc":
				m.menuOpen = false
				m.textarea.Reset()
				return m, nil
			case "enter", "tab":
				value := m.textarea.Value()
				m.textarea.Reset()
				if key == "tab" {
					// tab completes the command into the input.
					if m.menuSelected < len(m.menuItems) {
						m.textarea.SetValue(m.menuItems[m.menuSelected].text + " ")
						return m, nil
					}
				}
				// enter: if the typed input already names a full command
				// (possibly with arguments), run it directly; otherwise
				// enter selects the highlighted completion into the input.
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
					m.textarea.SetValue(typed)
					for _, line := range m.handleSlashInput(typed) {
						m.appendBlock(line)
					}
					m.refreshViewport()
					if m.quitRequested {
						return m, tea.Quit
					}
					return m, nil
				}
				if m.menuSelected < len(m.menuItems) {
					m.textarea.SetValue(m.menuItems[m.menuSelected].text + " ")
				}
				m.menuOpen = false
				return m, nil
			}
		}
		switch key {
		case "tab":
			// Toggle the most recent collapsed reasoning block.
			m.toggleReasoning()
			return m, nil
		case "ctrl+c":
			if m.busy && m.app != nil {
				m.app.Coordinator.Agent().Cancel(m.sessionID)
				m.status = "cancelling…"
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.pending != nil {
				return m, nil
			}
			return m, tea.Quit
		case "enter":
			if m.pending != nil {
				return m, nil
			}
			if key != "shift+enter" {
				value := strings.TrimSpace(m.textarea.Value())
				m.textarea.Reset()
				if value == "" || m.busy {
					return m, tea.Batch(cmds...)
				}
				m.appendBlock(userStyle.Render("❯ ") + value)
				if strings.HasPrefix(value, "/") {
					for _, line := range m.handleSlashInput(value) {
						m.appendBlock(line)
					}
					m.refreshViewport()
					if m.quitRequested {
						return m, tea.Quit
					}
					return m, tea.Batch(cmds...)
				}
				m.busy = true
				m.status = "thinking…"
				cmds = append(cmds, m.runAgent(m.sessionID, value))
				return m, tea.Batch(cmds...)
			}
		case "y", "n":
			if m.pending != nil {
				if key == "y" {
					m.app.Permissions.GrantPersistent(permission.PermissionRequest(*m.pending))
					m.appendBlock(toolStyle.Render("  → granted"))
				} else {
					m.app.Permissions.Deny(permission.PermissionRequest(*m.pending))
					m.appendBlock(toolStyle.Render("  → denied"))
				}
				m.pending = nil
				return m, nil
			}
		}

	case app.SubmitMsg:
		cmds = append(cmds, m.runAgent(msg.SessionID, msg.Prompt))

	case app.MessageUpdateMsg:
		m.handleMessageEvent(msg)

	case app.RunCompleteMsg:
		m.busy = false
		if msg.Error != "" {
			m.appendBlock(errorStyle.Render("error: " + msg.Error))
		}
		m.status = "ready"
		m.refreshViewport()

	case runErrorMsg:
		m.busy = false
		m.appendBlock(errorStyle.Render("error: " + msg.err.Error()))
		m.status = "ready"

	case runDoneMsg:
		m.busy = false
		m.status = "ready"
		m.refreshViewport()

	case app.PermissionRequestMsg:
		req := permission.PermissionRequest(msg)
		m.pending = &req
		m.appendBlock(permBoxStyle.Render(fmt.Sprintf(
			"⚙ %s  %s\n%s\npath: %s\n[y] grant (remember)   [n] deny",
			req.ToolName, req.Action, req.Description, req.Path)))

	case app.PermissionNotificationMsg:
		if msg.Denied {
			m.appendBlock(toolErrStyle.Render("  ✗ permission denied"))
		}
	}

	// Feed the textarea; live-trigger the slash menu on "/" input.
	valueBefore := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	valueAfter := m.textarea.Value()
	if !m.menuOpen && strings.HasPrefix(valueAfter, "/") && len(valueAfter) > len(valueBefore) {
		m.openSlashMenu(strings.TrimPrefix(valueAfter, "/"))
	} else if m.menuOpen {
		if strings.HasPrefix(valueAfter, "/") {
			m.openSlashMenu(strings.TrimPrefix(valueAfter, "/"))
		} else if valueAfter == "" {
			m.menuOpen = false
		}
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// toggleReasoning flips the collapsed state of the newest reasoning block.
func (m *Model) toggleReasoning() {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == blockReasoning {
			m.transcript[i].reasoningOpen = !m.transcript[i].reasoningOpen
			m.refreshViewport()
			return
		}
	}
}

// runAgent launches the agent turn in the background.
func (m *Model) runAgent(sessionID, userPrompt string) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		_, err := a.Coordinator.Agent().Run(context.Background(), agent.AgentCall{
			SessionID: sessionID,
			Prompt:    userPrompt,
		})
		if err != nil {
			return runErrorMsg{err}
		}
		return runDoneMsg{}
	}
}

type runErrorMsg struct{ err error }
type runDoneMsg struct{}

// handleMessageEvent renders streaming message updates into the transcript.
// The message service republishes the full message on every delta, so
// this must be idempotent: blocks are keyed by message ID and mutated
// in place instead of appended.
func (m *Model) handleMessageEvent(ev app.MessageUpdateMsg) {
	msg := ev.Payload
	switch msg.Role {
	case message.Assistant:
		sb := m.streamBlocksFor(msg.ID)

		// Reasoning: one thinking block per assistant message.
		if reasoning := msg.ReasoningContent().Thinking; reasoning != "" {
			if sb.reasoning < 0 {
				sb.reasoning = m.ensureBlock(blockReasoning)
			}
			m.transcript[sb.reasoning].content = reasoning
		}

		// Assistant text: one text block per assistant message.
		if text := msg.Content().Text; text != "" {
			if sb.text < 0 {
				sb.text = m.ensureBlock(blockText)
			}
			m.transcript[sb.text].content = text
		}

		// Tool calls: one line per call; skip ones already resolved by
		// an earlier result event (out-of-order pubsub fan-in).
		for _, tc := range msg.ToolCalls() {
			if m.toolResolved[tc.ID] {
				continue
			}
			m.appendToolCallLine(tc)
		}
		m.pullUsage(msg)
	case message.Tool:
		for _, tr := range msg.ToolResults() {
			m.resolveToolResult(tr)
		}
		m.pullUsage(msg)
	}
	m.status = statusForBusy(m.busy)
	m.refreshViewport()
}

// appendToolCallLine adds or updates the transcript line of a tool call.
func (m *Model) appendToolCallLine(tc message.ToolCall) {
	line := toolStyle.Render(fmt.Sprintf("  ⚙ %s%s", tc.Name, toolArgsPreview(tc.Input)))
	if !tc.Finished {
		line = toolStyle.Render(fmt.Sprintf("  ⚙ %s …", tc.Name))
	}
	if idx, ok := m.toolLines[tc.ID]; ok {
		m.transcript[idx] = block{content: line}
	} else {
		m.transcript = append(m.transcript, block{content: line})
		m.toolLines[tc.ID] = len(m.transcript) - 1
	}
}

// resolveToolResult updates the originating tool line with the outcome.
func (m *Model) resolveToolResult(tr message.ToolResult) {
	m.toolResolved[tr.ToolCallID] = true
	mark := "✓"
	style := statusStyle
	if tr.IsError {
		mark = "✗"
		style = toolErrStyle
	}
	summary := oneLine(tr.Content)
	if len([]rune(summary)) > 64 {
		summary = string([]rune(summary)[:64]) + "…"
	}
	line := toolStyle.Render(fmt.Sprintf("  ⚙ %s", tr.Name)) +
		style.Render(fmt.Sprintf("  %s %s", mark, summary))
	if idx, ok := m.toolLines[tr.ToolCallID]; ok {
		m.transcript[idx] = block{content: line}
	} else {
		m.transcript = append(m.transcript, block{content: line})
		m.toolLines[tr.ToolCallID] = len(m.transcript) - 1
	}
}

// toolArgsPreview renders a compact argument preview from raw JSON.
func toolArgsPreview(input string) string {
	if input == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"command", "file_path", "pattern", "path", "description"} {
		if v, ok := args[k].(string); ok {
			preview := oneLine(v)
			if len([]rune(preview)) > 48 {
				preview = string([]rune(preview)[:48]) + "…"
			}
			parts = append(parts, preview)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return statusStyle.Render("  " + strings.Join(parts, " · "))
}

// pullUsage updates token counters from persisted session stats.
func (m *Model) pullUsage(msg message.Message) {
	if m.app == nil {
		return
	}
	sess, err := m.app.Sessions.Get(context.Background(), msg.SessionID)
	if err != nil {
		return
	}
	m.tokensIn = sess.PromptTokens
	m.tokensOut = sess.CompletionTokens
	m.ctxWindow = m.app.Coordinator.Agent().CurrentModel().Cfg.ContextWindow
}

// statusForBusy returns the spinner label.
func statusForBusy(busy bool) string {
	if busy {
		return "◍ working…"
	}
	return "ready"
}

// View implements tea.Model.
func (m Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Loading…")
	}

	modelName := m.app.Coordinator.Agent().CurrentModelName()
	header := titleStyle.Render(" RakitKode ") +
		statusStyle.Render("  "+m.status) +
		statusStyle.Render("  ·  "+modelName)

	body := m.viewport.View()
	if m.pending != nil {
		body = lipgloss.JoinVertical(lipgloss.Left,
			body,
			permBoxStyle.Render("permission pending — [y] grant / [n] deny"),
		)
	}

	input := m.textarea.View()
	menu := m.renderSlashMenu()
	if menu != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, menu, input)
	}
	footer := borderStyle.Render(input)

	status := m.renderStatusBar()

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		body,
		footer,
		status,
	)
	return tea.NewView(content)
}

// renderStatusBar builds the metrics footer line.
func (m *Model) renderStatusBar() string {
	left := "enter send · shift+enter newline · tab thinking · / commands · ctrl+c quit/cancel"

	ctxPct := "-"
	if m.ctxWindow > 0 {
		used := m.tokensIn + m.tokensOut
		ctxPct = fmt.Sprintf("%.0f%%", float64(used)/float64(m.ctxWindow)*100)
	}
	right := fmt.Sprintf("↑ %s  ↓ %s  ctx %s",
		formatTokens(m.tokensIn), formatTokens(m.tokensOut), ctxPct)

	gap := m.width - len([]rune(stripAnsi(left))) - len([]rune(stripAnsi(right)))
	if gap < 1 {
		gap = 1
	}
	return statusStyle.Render(left + strings.Repeat(" ", gap) + right)
}

// formatTokens renders token counts in k format.
func formatTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// stripAnsi is a rough ANSI escape stripper for width math.
func stripAnsi(s string) string {
	var sb strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == '\033' {
			inEscape = true
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
