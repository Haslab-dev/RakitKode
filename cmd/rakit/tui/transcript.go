package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/charmbracelet/glamour"
)

var (
	titleStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212"))

	brandStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	reasoningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("179"))

	toolErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("174"))

	mcpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("176"))

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

	askBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("39")).
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

	// tool call rendering: ⏺ headline dot, tool name, dim args, ⎿ result mark
	toolDotRunStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	toolDotOKStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("71"))
	toolDotErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	toolNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("117"))

	resultMarkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	resultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
)

// blockKind identifies a block kind in the transcript.
type blockKind int

const (
	// blockPlain is an immutable transcript line (header, user input, status
	// notes).
	blockPlain blockKind = iota
	blockText
	blockReasoning
	blockTool
)

// maxToolVerbose caps the expanded tool output so a huge log cannot freeze
// the frame loop.
const maxToolVerbose = 20_000

// toolState is the lifecycle of a tool call block.
type toolState int

const (
	toolRunning toolState = iota
	toolOK
	toolErr
)

// toolInfo is the structured payload of a tool call block.
type toolInfo struct {
	name     string
	title    string // human label, e.g. "Bash", "Read", "server:tool"
	argsText string // "(ls -la)" — rendered dim after the title
	mcp      bool   // MCP-bridged tool (distinct color)
	state    toolState
	result   string
	diff     string
	isErr    bool
	expanded bool
	started  time.Time
	dur      time.Duration
}

// block is one replaceable line in the transcript (streamed content
// mutates in place instead of appending every delta).
type block struct {
	kind    blockKind
	content string
	// reasoningOpen tracks expanded state; reasoning starts expanded and
	// tab collapses it.
	reasoningOpen bool
	// sealed marks a stream that has finished; it separates the compact
	// streaming render of a reasoning block from the tab-expanded one.
	sealed bool
	// thinkingDur is how long the sealed reasoning block streamed.
	thinkingDur time.Duration
	// tool carries the structured tool call when kind == blockTool.
	tool *toolInfo
	// rendered caches the markdown rendering of a sealed text block
	// (invalidated on resize).
	rendered      string
	renderedWidth int
}

// appendBlock adds an immutable line to the transcript.
func (m *Model) appendBlock(content string) {
	m.transcript = append(m.transcript, block{kind: blockPlain, content: content})
	m.refreshViewport()
}

// ensureBlock appends a new block of the given kind and returns its index.
func (m *Model) ensureBlock(kind blockKind) int {
	m.transcript = append(m.transcript, block{kind: kind, reasoningOpen: kind == blockReasoning})
	return len(m.transcript) - 1
}

// streamText appends a text delta to the current streaming assistant block,
// opening a fresh block when no stream is open (turn start or post-tool).
func (m *Model) streamText(delta string) {
	if delta == "" {
		return
	}
	if !m.streamOpen {
		m.cur = streamBlocks{reasoning: -1, text: -1}
		m.streamOpen = true
	}
	if m.cur.text < 0 {
		m.cur.text = m.ensureBlock(blockText)
	}
	m.transcript[m.cur.text].content += delta
}

// streamThinking appends a reasoning delta to the current thinking block and
// starts the "thought for Ns" clock.
func (m *Model) streamThinking(delta string) {
	if delta == "" {
		return
	}
	if !m.streamOpen {
		m.cur = streamBlocks{reasoning: -1, text: -1}
		m.streamOpen = true
	}
	if m.cur.reasoning < 0 {
		m.cur.reasoning = m.ensureBlock(blockReasoning)
		m.thinkStart = time.Now()
	}
	m.transcript[m.cur.reasoning].content += delta
}

// closeStream seals the current streaming blocks: reasoning collapses (tab
// re-expands) with its duration, and the assistant text is rendered as
// markdown once.
func (m *Model) closeStream() {
	if !m.streamOpen {
		return
	}
	if m.cur.reasoning >= 0 && m.cur.reasoning < len(m.transcript) {
		b := &m.transcript[m.cur.reasoning]
		b.reasoningOpen = false
		b.sealed = true
		b.thinkingDur = time.Since(m.thinkStart).Truncate(time.Second)
	}
	if m.cur.text >= 0 && m.cur.text < len(m.transcript) {
		b := &m.transcript[m.cur.text]
		b.rendered = renderMarkdown(b.content, m.markdownWidth())
		b.renderedWidth = m.markdownWidth()
	}
	m.streamOpen = false
}

// markdownWidth is the wrap width for assistant markdown: the viewport
// width minus a small gutter, clamped to a readable minimum.
func (m *Model) markdownWidth() int {
	w := m.width - 8
	if w < 40 {
		w = 40
	}
	if w > 120 {
		w = 120
	}
	return w
}

// previewWidth is the wrap width for tool result previews and expanded
// output.
func (m *Model) previewWidth() int {
	w := m.width - 12
	if w < 40 {
		w = 40
	}
	if w > 140 {
		w = 140
	}
	return w
}

// invalidateRendered drops cached markdown renders after a resize so blocks
// re-wrap at the new width.
func (m *Model) invalidateRendered() {
	for i := range m.transcript {
		m.transcript[i].rendered = ""
	}
}

// mdRendererCache holds one shared glamour renderer per wrap width; creating
// a renderer parses style sheets, so it must not happen per frame.
var (
	mdMu             sync.Mutex
	mdWidth          int
	mdCachedRenderer *glamour.TermRenderer
)

func mdRenderer(width int) (*glamour.TermRenderer, error) {
	mdMu.Lock()
	defer mdMu.Unlock()
	if mdCachedRenderer == nil || mdWidth != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
			glamour.WithEmoji(),
		)
		if err != nil {
			return nil, err
		}
		mdCachedRenderer, mdWidth = r, width
	}
	return mdCachedRenderer, nil
}

// renderMarkdown renders assistant text with the shared renderer. Glamour is
// safe for sequential Renders; transcript refreshes happen on the UI
// goroutine only, so no extra locking around Render is needed.
func renderMarkdown(src string, width int) string {
	if strings.TrimSpace(src) == "" {
		return src
	}
	r, err := mdRenderer(width)
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	return strings.Trim(out, "\n")
}

// refreshViewport re-renders all blocks and syncs the scroll area. Scrolling
// sticks to the bottom until the user deliberately scrolls up (pgup / wheel
// up) and resumes once they return to the bottom — the widget's own AtBottom
// answer is not trusted, since soft-wrap accounting can leave it off by a
// wrapped sliver row.
func (m *Model) refreshViewport() {
	var sb strings.Builder
	for i := range m.transcript {
		sb.WriteString(m.renderBlock(i))
		sb.WriteString("\n")
	}
	// While a turn is busy and nothing has streamed yet, an inline activity
	// line keeps the wait visible next to the user's message — not just in
	// the top header.
	if m.busy && m.awaitFirstDelta {
		sb.WriteString(m.activityLine())
	}
	m.viewport.SetContent(strings.TrimSuffix(sb.String(), "\n"))
	if m.stuckBottom {
		m.viewport.GotoBottom()
	}
}

// activityLine is the inline "still working" indicator shown between the
// user's message and the model's first streamed output.
func (m Model) activityLine() string {
	return m.spinner.View() + statusStyle.Render(" "+m.verb()+"…")
}

// renderBlock renders one transcript entry.
func (m *Model) renderBlock(i int) string {
	b := &m.transcript[i]
	switch b.kind {
	case blockReasoning:
		return m.renderReasoningBlock(i)
	case blockText:
		// While this block is the live stream, show raw text with a cursor
		// — no manual wrapping: the viewport soft-wraps it, and re-wrapping
		// a growing block per delta is what made streaming crawl.
		if m.streamOpen && i == m.cur.text {
			return b.content + "▋"
		}
		if b.rendered != "" {
			return b.rendered
		}
		if w := m.markdownWidth(); b.renderedWidth != w && strings.TrimSpace(b.content) != "" {
			b.rendered = renderMarkdown(b.content, w)
			b.renderedWidth = w
			return b.rendered
		}
		return m.plainWrap(b.content)
	case blockTool:
		return m.renderToolBlock(i)
	default:
		return b.content
	}
}

// reasoningTailLines is how many lines of a streaming reasoning block render
// below the header. Keeping the block compact means the ✻ header stays on
// screen next to the action instead of scrolling away above a wall of text.
const reasoningTailLines = 3

// renderReasoningBlock renders the thinking block in one of three states:
//
//	streaming   ✻ thinking… + the last few reasoning lines (compact)
//	collapsed   ✻ thought for 3s · preview (tab expands)
//	expanded    ✻ thought for 3s (tab collapses) + the full body
func (m *Model) renderReasoningBlock(i int) string {
	b := &m.transcript[i]

	if b.reasoningOpen && !b.sealed {
		if strings.TrimSpace(b.content) == "" {
			return reasoningStyle.Render("✻ thinking…")
		}
		lines := nonEmptyLines(b.content)
		header := "✻ thinking…"
		if len(lines) > reasoningTailLines {
			header += fmt.Sprintf(" (%d lines)", len(lines))
		}
		header += "  (tab collapses)"
		tail := lines
		if len(tail) > reasoningTailLines {
			tail = tail[len(tail)-reasoningTailLines:]
		}
		return reasoningStyle.Render(header) + "\n" + reasoningStyle.Render(indent(strings.Join(tail, "\n"), "│ "))
	}

	if b.reasoningOpen {
		header := "✻ thought"
		if b.thinkingDur > 0 {
			header += " for " + b.thinkingDur.String()
		}
		body := reasoningStyle.Render(indent(strings.TrimRight(b.content, "\n"), "│ "))
		return reasoningStyle.Render(header+"  (tab collapses)") + "\n" + body
	}

	dur := ""
	if b.thinkingDur > 0 {
		dur = fmt.Sprintf(" for %s", b.thinkingDur)
	}
	preview := oneLine(b.content)
	runes := []rune(preview)
	max := 72
	if len(runes) > max {
		preview = string(runes[:max]) + "…"
	}
	if preview == "" {
		return reasoningStyle.Render(fmt.Sprintf("✻ thought%s (tab expands)", dur))
	}
	return reasoningStyle.Render(fmt.Sprintf("✻ thought%s · %s… (tab expands)", dur, preview))
}

// renderToolBlock renders a tool call Claude-Code style: a ⏺ headline with
// the tool label and arguments, then a ⎿ result block (3-line preview, full
// output when expanded, duration once finished).
func (m *Model) renderToolBlock(i int) string {
	t := m.transcript[i].tool
	if t == nil {
		return ""
	}

	dotStyle := toolDotOKStyle
	switch t.state {
	case toolRunning:
		dotStyle = toolDotRunStyle
	case toolErr:
		dotStyle = toolDotErrStyle
	}

	head := t.title
	if t.state == toolRunning {
		head += "…"
	}
	nameStyle := toolNameStyle
	if t.mcp {
		nameStyle = mcpStyle
	}
	line := dotStyle.Render("⏺") + " " + nameStyle.Render(head) + statusStyle.Render(t.argsText)
	if t.state == toolOK && t.dur > 0 {
		line += statusStyle.Render("  (" + t.dur.String() + ")")
	}

	// Running calls have no result yet.
	if t.state == toolRunning {
		return line
	}

	body := strings.TrimSpace(t.result)
	if body == "" {
		body = "(no output)"
	}
	if t.isErr {
		return line + "\n" + toolErrStyle.Render("  ⎿  ✗ "+m.truncateWidth(oneLine(body)))
	}

	// Edit/write results carry a unified diff; render it with added/removed
	// coloring instead of a plain preview.
	if t.diff != "" {
		return line + "\n" + m.renderDiff(t.diff, t.expanded)
	}

	if t.expanded {
		text := strings.TrimRight(t.result, "\n")
		if len(text) > maxToolVerbose {
			text = text[:maxToolVerbose] + "\n… output truncated"
		}
		// Raw text: the viewport soft-wraps it; only expanded blocks pay
		// for it and they re-render per refresh.
		return line + "\n" + resultMarkStyle.Render("  ⎿  ") + text
	}

	lines := nonEmptyLines(body)
	show := lines
	if len(show) > 3 {
		show = show[:3]
	}
	var sb strings.Builder
	for j, ln := range show {
		mark := "     "
		if j == 0 {
			mark = "  ⎿  "
		}
		sb.WriteString("\n" + resultMarkStyle.Render(mark) + resultStyle.Render(m.truncateWidth(ln)))
	}
	if extra := len(lines) - len(show); extra > 0 {
		sb.WriteString(statusStyle.Render(fmt.Sprintf("\n     … + %d more (ctrl+o expands)", extra)))
	}
	return line + sb.String()
}

// plainWrap hard-wraps plain streamed text to the viewport width so long
// tokens don't overflow the frame.
func (m *Model) plainWrap(s string) string {
	w := m.markdownWidth()
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for len([]rune(line)) > w {
			out = append(out, string([]rune(line)[:w]))
			line = string([]rune(line)[w:])
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// truncateWidth clips one line to the preview width.
func (m *Model) truncateWidth(s string) string {
	runes := []rune(s)
	w := m.previewWidth()
	if len(runes) <= w {
		return s
	}
	return string(runes[:w]) + "…"
}

// renderDiff renders a unified diff with added/removed coloring. Collapsed
// mode shows the first 12 lines plus a summary.
func (m *Model) renderDiff(diff string, expanded bool) string {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	limit := len(lines)
	if !expanded && limit > 12 {
		limit = 12
	}
	var sb strings.Builder
	added, removed := 0, 0
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			continue
		case strings.HasPrefix(ln, "+"):
			added++
		case strings.HasPrefix(ln, "-"):
			removed++
		}
		if i >= limit {
			continue
		}
		style := resultStyle
		switch {
		case strings.HasPrefix(ln, "+"):
			style = toolDotOKStyle
		case strings.HasPrefix(ln, "-"):
			style = toolDotErrStyle
		case strings.HasPrefix(ln, "@@"):
			style = statusStyle
		}
		mark := "     "
		if i == 0 {
			mark = "  ⎿  "
		}
		sb.WriteString("\n" + resultMarkStyle.Render(mark) + style.Render(m.truncateWidth(ln)))
	}
	if !expanded && len(lines) > limit {
		sb.WriteString(statusStyle.Render(fmt.Sprintf("\n     … + %d more (ctrl+o expands)", len(lines)-limit)))
	}
	sb.WriteString(statusStyle.Render(fmt.Sprintf("\n     +%d −%d", added, removed)))
	return sb.String()
}

// setToolDiff attaches a unified diff to a tool block.
func (m *Model) setToolDiff(id, diff string) {
	idx, ok := m.toolLines[id]
	if !ok || idx >= len(m.transcript) || m.transcript[idx].tool == nil {
		return
	}
	m.transcript[idx].tool.diff = diff
	m.refreshViewport()
}

// hydrateTranscript rebuilds the transcript from the session's stored
// messages so --continue / --session shows the real history.
func (m *Model) hydrateTranscript() {
	if m.mgr == nil {
		return
	}
	sess, ok := m.mgr.GetSession(m.sessionID)
	if !ok {
		return
	}
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "user":
			for _, b := range msg.Content {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					m.appendBlock(userStyle.Render("❯ ") + b.Text)
				}
			}
		case "assistant":
			for _, b := range msg.Content {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					idx := m.ensureBlock(blockText)
					m.transcript[idx].content = b.Text
					m.transcript[idx].sealed = true
					m.transcript[idx].rendered = renderMarkdown(b.Text, m.markdownWidth())
					m.transcript[idx].renderedWidth = m.markdownWidth()
				case "reasoning", "thinking":
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					idx := m.ensureBlock(blockReasoning)
					m.transcript[idx].content = b.Text
					m.transcript[idx].reasoningOpen = false
					m.transcript[idx].sealed = true
				case "tool_use", "tool_call":
					info := newToolInfo(b.Name, b.Arguments)
					info.state = toolOK
					idx := m.ensureBlock(blockTool)
					m.transcript[idx].tool = info
					if b.ToolCallID != "" {
						m.toolLines[b.ToolCallID] = idx
					}
				}
			}
		case "tool":
			for _, b := range msg.Content {
				if b.Type != "tool_result" {
					continue
				}
				idx, ok := m.toolLines[b.ToolCallID]
				if !ok || idx >= len(m.transcript) || m.transcript[idx].tool == nil {
					continue
				}
				t := m.transcript[idx].tool
				t.result = b.Text
				t.isErr = b.IsError
				if b.IsError {
					t.state = toolErr
				} else {
					t.state = toolOK
				}
			}
		}
	}
	m.refreshViewport()
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

// toggleToolOutput flips the expanded state of the newest finished tool
// block (ctrl+o).
func (m *Model) toggleToolOutput() {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == blockTool && m.transcript[i].tool != nil && m.transcript[i].tool.state != toolRunning {
			m.transcript[i].tool.expanded = !m.transcript[i].tool.expanded
			m.refreshViewport()
			return
		}
	}
}

// setToolRunning adds or updates the structured block of a running tool call.
func (m *Model) setToolRunning(id, name string, args map[string]any) {
	if id == "" {
		return
	}
	info := newToolInfo(name, args)
	m.setToolLine(id, block{kind: blockTool, tool: info})
}

// setToolResult updates the originating tool block with the outcome.
func (m *Model) setToolResult(id, name, result string, isErr bool) {
	if id == "" {
		return
	}
	if idx, ok := m.toolLines[id]; ok && idx < len(m.transcript) && m.transcript[idx].tool != nil {
		t := m.transcript[idx].tool
		t.result = result
		t.isErr = isErr
		if isErr {
			t.state = toolErr
		} else {
			t.state = toolOK
		}
		if !t.started.IsZero() {
			t.dur = time.Since(t.started).Truncate(100 * time.Millisecond)
		}
		return
	}
	// Result arrived without a tracked start (resumed session, dropped
	// event): render a finished block directly.
	info := newToolInfo(name, nil)
	info.result = result
	info.isErr = isErr
	if isErr {
		info.state = toolErr
	} else {
		info.state = toolOK
	}
	m.setToolLine(id, block{kind: blockTool, tool: info})
}

// setToolLine adds or replaces the transcript block keyed by a tool call id.
func (m *Model) setToolLine(id string, b block) {
	if idx, ok := m.toolLines[id]; ok && idx < len(m.transcript) {
		// Preserve the expand toggle across updates.
		if m.transcript[idx].tool != nil && b.tool != nil {
			b.tool.expanded = m.transcript[idx].tool.expanded
		}
		m.transcript[idx] = b
		return
	}
	m.transcript = append(m.transcript, b)
	m.toolLines[id] = len(m.transcript) - 1
}

// toolTitles maps canonical tool names to display labels.
var toolTitles = map[string]string{
	"bash":            "Bash",
	"read":            "Read",
	"read_multiple":   "Read",
	"write":           "Write",
	"edit":            "Edit",
	"search":          "Search",
	"find":            "Find",
	"glob":            "Glob",
	"git_status":      "GitStatus",
	"todo":            "Todo",
	"update_tasks":    "Tasks",
	"ask":             "Ask",
	"load_skill":      "Skill",
	"create_skill":    "CreateSkill",
	"list_skills":     "ListSkills",
	"create_plugin":   "CreatePlugin",
	"list_plugins":    "ListPlugins",
	"toggle_plugin":   "TogglePlugin",
	"learn_memory":    "Learn",
	"recall_memory":   "Recall",
	"invoke_subagent": "Task",
	"index":           "Index",
}

// toolArgKeys lists, per tool, the argument keys worth echoing in the
// headline (first match wins).
var toolArgKeys = map[string][]string{
	"bash":            {"command", "cmd"},
	"read":            {"file_path", "path", "file"},
	"read_multiple":   {"paths", "file_paths"},
	"write":           {"file_path", "path", "target_file"},
	"edit":            {"file_path", "path", "target_file"},
	"search":          {"pattern", "query"},
	"find":            {"pattern", "query"},
	"glob":            {"pattern"},
	"load_skill":      {"name", "skill"},
	"invoke_subagent": {"description", "task"},
	"recall_memory":   {"query"},
	"learn_memory":    {"content", "text"},
}

// newToolInfo builds the structured rendering payload for a tool call.
func newToolInfo(name string, args map[string]any) *toolInfo {
	info := &toolInfo{name: name, state: toolRunning, started: time.Now()}
	if server, tool, ok := splitMCPName(name); ok {
		info.mcp = true
		info.title = server + ":" + tool
		info.argsText = "  " + firstStringArg(args)
		return info
	}
	title, ok := toolTitles[name]
	if !ok {
		title = name
	}
	info.title = title
	if keys, ok := toolArgKeys[name]; ok {
		info.argsText = "(" + firstStringArg(args, keys...) + ")"
	} else {
		info.argsText = "  " + firstStringArg(args)
	}
	return info
}

// splitMCPName recognizes MCP-bridged tool names in either registered form:
// "server/tool" or the alias "mcp__server__tool".
func splitMCPName(name string) (server, tool string, ok bool) {
	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
		return "", "", false
	}
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
	}
	return "", "", false
}

// firstStringArg returns the first present string argument, trying keys in
// order and falling back to the first string value in the map.
func firstStringArg(args map[string]any, keys ...string) string {
	if args == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return oneLine(v)
		}
	}
	// Deterministic fallback: alphabetical key order.
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sortStrings(names)
	for _, k := range names {
		if v, ok := args[k].(string); ok && v != "" {
			return oneLine(v)
		}
	}
	return ""
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// nonEmptyLines returns the trimmed, non-empty lines of s.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimRight(line, " \t\r"); strings.TrimSpace(t) != "" {
			out = append(out, t)
		}
	}
	return out
}

// argsPreview renders a compact argument preview from the call's arguments
// (used by the approval box; the transcript uses structured headlines).
func argsPreview(args map[string]any) string {
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
	return strings.Join(parts, " · ")
}

// appendSessionHeader is the banner shown on start and after /clear. The
// identity line lives in the app header, so the transcript keeps a single
// compact hint line.
func (m *Model) appendSessionHeader() {
	m.appendBlock(statusStyle.Render("  /help commands · /model pick model · type to ask · ctrl+c quit"))
	m.appendBlock("")
}

// renderApprovalBox builds the inline tool-approval prompt.
func (m Model) renderApprovalBox() string {
	rows := []string{headerStyle.Render("tool approval")}
	for i, tc := range m.pendingApproval {
		num := fmt.Sprintf("  %d) ", i+1)
		if len(m.pendingApproval) == 1 {
			num = "  "
		}
		rows = append(rows, toolStyle.Render(num+"⚙ "+tc.Name)+
			statusStyle.Render("  "+argsPreview(tc.Arguments)))
	}
	rows = append(rows, statusStyle.Render("[y] grant · [a] grant always · [n] deny"))
	return permBoxStyle.Render(strings.Join(rows, "\n"))
}

// renderAskBox builds the inline question prompt for the current question.
func (m Model) renderAskBox() string {
	a := m.pendingAsk
	if a == nil || a.qi >= len(a.questions) {
		return ""
	}
	q := a.questions[a.qi]
	title := fmt.Sprintf("question %d/%d", a.qi+1, len(a.questions))
	if q.Header != "" {
		title += " — " + q.Header
	}
	rows := []string{headerStyle.Render(title), q.Question}
	picked := map[string]bool{}
	for _, o := range a.picks[q.ID] {
		picked[o] = true
	}
	for i, opt := range q.Options {
		marker := " "
		switch {
		case picked[opt]:
			marker = "●"
		case i == q.Recommended:
			marker = "•"
		}
		rows = append(rows, fmt.Sprintf("  %s %d) %s", marker, i+1, opt))
	}
	hint := "1-9 answer · enter confirm"
	if q.Multi {
		hint = "1-9 toggle (pick all) · enter confirm"
	}
	rows = append(rows, statusStyle.Render(hint))
	return askBoxStyle.Render(strings.Join(rows, "\n"))
}

// renderStatusBar builds the hints + usage footer line. Hints degrade
// gracefully on narrow terminals: the usage counters on the right always
// win, and the longest hint set that fits is shown on the left.
func (m Model) renderStatusBar() string {
	scroll := ""
	if !m.viewport.AtTop() {
		scroll += "↑ "
	}
	if !m.stuckBottom {
		scroll += "↓ "
	}

	right := fmt.Sprintf("↑ %s  ↓ %s", formatTokens(int64(m.tokensIn)), formatTokens(int64(m.tokensOut)))
	if m.contextTokens > 0 {
		right += fmt.Sprintf("  ctx %s", formatTokens(int64(m.contextTokens)))
	}
	if m.cacheHit > 0 {
		right += fmt.Sprintf("  cache %.0f%%", m.cacheHit*100)
	}
	if m.estimatedCost > 0 {
		right += fmt.Sprintf("  %s", llm.FormatCost(m.estimatedCost))
	}
	used := len([]rune(stripAnsi(right))) + len(scroll)

	left := ""
	for _, candidate := range []string{
		"enter send · tab thinking · ctrl+o output · / cmds · esc quit",
		"enter send · tab thinking · / commands · esc interrupt",
		"enter send · / commands · esc quit",
		"",
	} {
		if len([]rune(candidate))+used <= m.width-3 {
			left = candidate
			break
		}
	}
	if left == "" {
		return statusStyle.Render(scroll + right)
	}
	if scroll != "" {
		left = scroll + "scroll · " + left
	}

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

// indent prefixes every non-empty line of s.
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
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
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
