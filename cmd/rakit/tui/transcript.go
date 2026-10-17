package tui

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
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
)

// blockKind identifies a block kind in the transcript.
type blockKind int

const (
	// blockPlain is an immutable transcript line (header, user input, tool
	// lines, status notes).
	blockPlain blockKind = iota
	blockText
	blockReasoning
)

// block is one replaceable line in the transcript (streamed content
// mutates in place instead of appending every delta).
type block struct {
	kind    blockKind
	content string
	// reasoningOpen tracks expanded state; reasoning starts expanded and
	// tab collapses it.
	reasoningOpen bool
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

// streamThinking appends a reasoning delta to the current thinking block.
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
	}
	m.transcript[m.cur.reasoning].content += delta
}

// closeStream seals the current streaming blocks: reasoning collapses (tab
// re-expands) and the assistant text is rendered as markdown once.
func (m *Model) closeStream() {
	if !m.streamOpen {
		return
	}
	if m.cur.reasoning >= 0 && m.cur.reasoning < len(m.transcript) {
		m.transcript[m.cur.reasoning].reasoningOpen = false
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
			if strings.TrimSpace(b.content) == "" {
				return reasoningStyle.Render(label + "…")
			}
			body := reasoningStyle.Render(indent(b.content, "│ "))
			return reasoningStyle.Render(label+"  (tab collapses)") + "\n" + body
		}
		preview := oneLine(b.content)
		if len([]rune(preview)) > 88 {
			preview = string([]rune(preview)[:88]) + "…"
		}
		if preview == "" {
			return ""
		}
		return reasoningStyle.Render(fmt.Sprintf("%s (tab expands) · %s", label, preview))
	case blockText:
		// While this block is the live stream, show raw text with a cursor;
		// once sealed, show the cached markdown render.
		if m.streamOpen && i == m.cur.text {
			return m.plainWrap(b.content + "▋")
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
	default:
		return b.content
	}
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

// setToolRunning adds or updates the transcript line of a running tool call.
func (m *Model) setToolRunning(id, name string, args map[string]any) {
	if id == "" {
		return
	}
	line := toolStyle.Render("  ⚙ "+name) + statusStyle.Render("  "+argsPreview(args))
	m.setToolLine(id, line)
}

// setToolResult updates the originating tool line with the outcome.
func (m *Model) setToolResult(id, name, result string, isErr bool) {
	if id == "" {
		return
	}
	mark, style := "✓", statusStyle
	if isErr {
		mark, style = "✗", toolErrStyle
	}
	summary := oneLine(result)
	if len([]rune(summary)) > 64 {
		summary = string([]rune(summary)[:64]) + "…"
	}
	m.setToolLine(id, toolStyle.Render("  ⚙ "+name)+style.Render("  "+mark+" "+summary))
}

// setToolLine adds or replaces the transcript line keyed by a tool call id.
func (m *Model) setToolLine(id, line string) {
	if idx, ok := m.toolLines[id]; ok && idx < len(m.transcript) {
		m.transcript[idx] = block{kind: blockPlain, content: line}
		return
	}
	m.transcript = append(m.transcript, block{kind: blockPlain, content: line})
	m.toolLines[id] = len(m.transcript) - 1
}

// argsPreview renders a compact argument preview from the call's arguments.
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

// appendSessionHeader is the banner shown on start and after /clear: one
// compact identity line plus keybind hints.
func (m *Model) appendSessionHeader() {
	m.appendBlock(brandStyle.Render("✳ Rakit") + statusStyle.Render("  ·  "+m.headerDetail()))
	m.appendBlock(statusStyle.Render("  /model pick model · / commands · shift+enter newline · tab thinking · esc interrupt"))
	m.appendBlock("")
}

// headerDetail describes the session: model and workspace.
func (m *Model) headerDetail() string {
	if m.mgr == nil {
		return "display only (no engine)"
	}
	model := m.modelName
	if model == "" {
		model = "unknown model"
	}
	ws := ""
	if m.eng != nil {
		ws = m.eng.Workspace()
	}
	if ws != "" {
		return fmt.Sprintf("model %s · workspace %s", model, ws)
	}
	return "model " + model
}

// renderApprovalBox builds the inline tool-approval prompt.
func (m Model) renderApprovalBox() string {
	rows := []string{headerStyle.Render("approve tool call")}
	for _, tc := range m.pendingApproval {
		rows = append(rows, toolStyle.Render("  ⚙ "+tc.Name)+
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

// renderStatusBar builds the hints + usage footer line.
func (m Model) renderStatusBar() string {
	scroll := ""
	if !m.viewport.AtTop() {
		scroll += "↑ "
	}
	if !m.viewport.AtBottom() {
		scroll += "↓ "
	}
	left := "enter send · tab thinking · / commands · esc interrupt · ctrl+c quit"
	if scroll != "" {
		left = scroll + " scroll  ·  " + left
	}

	right := fmt.Sprintf("↑ %s  ↓ %s", formatTokens(int64(m.tokensIn)), formatTokens(int64(m.tokensOut)))
	if m.cacheHit > 0 {
		right += fmt.Sprintf("  cache %.0f%%", m.cacheHit*100)
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
