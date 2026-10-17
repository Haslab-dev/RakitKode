package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/tools"
)

// newTestModel builds the display-only model used when no engine is attached;
// exercising it proves the TUI constructs and updates without panicking.
func newTestModel(t *testing.T) Model {
	t.Helper()
	return New(nil, "test-session")
}

// resize applies a WindowSizeMsg to the model and returns the updated model.
func resize(m Model, w, h int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(Model)
}

// busEv builds a bus event for the test session.
func busEv(t events.EventType, data map[string]any) busEvent {
	if data == nil {
		data = map[string]any{}
	}
	data["session_id"] = "test-session"
	return busEvent{ev: events.Event{Type: t, Data: data}}
}

// toolBlockByID returns the structured tool block tracked under id.
func toolBlockByID(m Model, id string) (*toolInfo, bool) {
	idx, ok := m.toolLines[id]
	if !ok || idx >= len(m.transcript) {
		return nil, false
	}
	return m.transcript[idx].tool, true
}

func TestNewModelBuildsHeader(t *testing.T) {
	m := newTestModel(t)
	if len(m.transcript) == 0 {
		t.Fatal("expected session header block in transcript")
	}
	if !strings.Contains(m.transcript[0].content, "/help") {
		t.Fatalf("header block missing hints, got %q", m.transcript[0].content)
	}
}

func TestViewAndResizeWithoutEngine(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	if !m.ready {
		t.Fatal("model not ready after WindowSizeMsg")
	}
	view := m.View().Content
	if !strings.Contains(view, "rakit") {
		t.Fatal("view missing header")
	}
	if !strings.Contains(view, "ready") {
		t.Fatal("view missing status")
	}
}

func TestStreamDeltasReuseBlocks(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)

	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "hel"}).ev)
	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "lo"}).ev)

	var textBlocks int
	for _, b := range m.transcript {
		if b.kind == blockText {
			textBlocks++
			if b.content != "hello" {
				t.Fatalf("text block content = %q, want %q", b.content, "hello")
			}
		}
	}
	if textBlocks != 1 {
		t.Fatalf("got %d text blocks, want 1 (deltas must mutate in place)", textBlocks)
	}

	// Thinking streams into its own expanded block (tab collapses it).
	m.handleBusEvent(busEv(events.AgentThinkingDelta, map[string]any{"delta": "pondering"}).ev)
	if m.cur.reasoning < 0 {
		t.Fatal("thinking block not opened")
	}
	if !m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("reasoning should start expanded")
	}
}

func TestToolStreamLifecycle(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)

	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "running…"}).ev)
	m.handleBusEvent(busEv(events.AgentToolStart, map[string]any{
		"tool_call_id": "t1", "name": "bash", "args": map[string]any{"command": "ls -la"},
	}).ev)
	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "done"}).ev)
	m.handleBusEvent(busEv(events.AgentToolEnd, map[string]any{
		"tool_call_id": "t1", "name": "bash", "result": "file.txt", "is_error": false,
	}).ev)

	// The text after tool_start must start a NEW block, not append to the
	// pre-tool text.
	texts := 0
	for _, b := range m.transcript {
		if b.kind == blockText {
			texts++
		}
	}
	if texts != 2 {
		t.Fatalf("got %d text blocks, want 2 (stream must close at tool_start)", texts)
	}

	info, ok := toolBlockByID(m, "t1")
	if !ok {
		t.Fatal("tool block not tracked")
	}
	if info.state != toolOK {
		t.Fatalf("tool state = %v, want toolOK", info.state)
	}
	if info.title != "Bash" || info.argsText != "(ls -la)" {
		t.Fatalf("headline = %q%q, want Bash(ls -la)", info.title, info.argsText)
	}
	rendered := m.renderToolBlock(m.toolLines["t1"])
	// Style codes sit between the title and the args, so match them apart.
	if !strings.Contains(rendered, "Bash") || !strings.Contains(rendered, "(ls -la)") {
		t.Fatalf("rendered block missing headline: %q", rendered)
	}
	if !strings.Contains(rendered, "file.txt") {
		t.Fatalf("tool line missing result preview: %q", rendered)
	}
}

func TestToolResultWithoutStartRendersFinishedBlock(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	m.handleBusEvent(busEv(events.AgentToolEnd, map[string]any{
		"tool_call_id": "late", "name": "search", "result": "a.go\nb.go", "is_error": false,
	}).ev)
	info, ok := toolBlockByID(m, "late")
	if !ok {
		t.Fatal("late result not tracked")
	}
	if info.state != toolOK {
		t.Fatalf("state = %v, want toolOK", info.state)
	}
}

func TestToolErrorRendersErrorMark(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	m.handleBusEvent(busEv(events.AgentToolStart, map[string]any{
		"tool_call_id": "e1", "name": "bash", "args": map[string]any{"command": "boom"},
	}).ev)
	m.handleBusEvent(busEv(events.AgentToolEnd, map[string]any{
		"tool_call_id": "e1", "name": "bash", "result": "exit 1", "is_error": true,
	}).ev)
	info, _ := toolBlockByID(m, "e1")
	if info.state != toolErr {
		t.Fatalf("state = %v, want toolErr", info.state)
	}
	rendered := m.renderToolBlock(m.toolLines["e1"])
	if !strings.Contains(rendered, "✗") {
		t.Fatalf("error mark missing: %q", rendered)
	}
}

func TestMCPToolBlockIsDistinct(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	m.handleBusEvent(busEv(events.AgentToolStart, map[string]any{
		"tool_call_id": "m1", "name": "mcp__context7__get-docs", "args": map[string]any{"q": "x"},
	}).ev)
	m.handleBusEvent(busEv(events.AgentToolEnd, map[string]any{
		"tool_call_id": "m1", "name": "mcp__context7__get-docs", "result": "ok", "is_error": false,
	}).ev)
	info, ok := toolBlockByID(m, "m1")
	if !ok {
		t.Fatal("mcp tool block not tracked")
	}
	if !info.mcp {
		t.Fatal("mcp flag not set for mcp__-prefixed tool")
	}
	if info.title != "context7:get-docs" {
		t.Fatalf("title = %q, want context7:get-docs", info.title)
	}

	// "server/tool" registered form is recognized too.
	if _, tool, ok := splitMCPName("context7/get-docs"); !ok || tool != "get-docs" {
		t.Fatalf("splitMCPName(server/tool) failed")
	}
	if _, _, ok := splitMCPName("bash"); ok {
		t.Fatal("core tool misdetected as MCP")
	}
}

func TestToolOutputExpandToggle(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	long := strings.Repeat("line\n", 12)
	m.handleBusEvent(busEv(events.AgentToolStart, map[string]any{
		"tool_call_id": "x1", "name": "bash", "args": map[string]any{"command": "seq"},
	}).ev)
	m.handleBusEvent(busEv(events.AgentToolEnd, map[string]any{
		"tool_call_id": "x1", "name": "bash", "result": long, "is_error": false,
	}).ev)

	idx := m.toolLines["x1"]
	collapsed := m.renderToolBlock(idx)
	if !strings.Contains(collapsed, "+ 9 more") {
		t.Fatalf("collapsed preview should note hidden lines: %q", collapsed)
	}
	m.toggleToolOutput()
	if !m.transcript[idx].tool.expanded {
		t.Fatal("ctrl+o should expand the newest finished tool block")
	}
	expanded := m.renderToolBlock(idx)
	if strings.Contains(expanded, "+ 9 more") {
		t.Fatalf("expanded render should show full output, not the note: %q", expanded)
	}
	if strings.Count(expanded, "line") != 12 {
		t.Fatalf("expanded render should contain all 12 lines: %q", expanded)
	}
}

func TestTurnEndReportsErrorAndResetsBusy(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.handleBusEvent(busEv(events.AgentTurnEnd, map[string]any{"error": "context canceled"}).ev)
	if m.busy {
		t.Fatal("busy should reset on turn end")
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.content, "stopped by user") {
		t.Fatalf("cancellation line missing, got %q", last.content)
	}

	m.busy = true
	m.handleBusEvent(busEv(events.AgentTurnEnd, map[string]any{"error": "LLM exploded"}).ev)
	last = m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.content, "LLM exploded") {
		t.Fatalf("error line missing, got %q", last.content)
	}
	if m.busy {
		t.Fatal("busy should reset on turn end")
	}
}

func TestApprovalPromptSwallowsKeysWithoutEngine(t *testing.T) {
	m := newTestModel(t)
	m.pendingApproval = []agent.ContentBlock{{Type: "tool_call", ToolCallID: "t1", Name: "write"}}

	// Typing must be swallowed while the prompt is open; only y/a/n answer,
	// and with no engine they clear the prompt harmlessly.
	handled, _ := m.interceptKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled {
		t.Fatal("keys must be consumed while approval is pending")
	}
	if m.textarea.Value() != "" {
		t.Fatal("keystroke leaked into input during approval prompt")
	}
	handled, _ = m.interceptKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !handled || len(m.pendingApproval) != 0 {
		t.Fatal("y should answer and clear the approval prompt")
	}
}

func TestAskPromptNumberKeysAnswer(t *testing.T) {
	m := newTestModel(t)
	m.pendingAsk = newAskState([]tools.AskQuestion{{
		ID:       "q1",
		Question: "which db?",
		Options:  []string{"postgres", "sqlite"},
	}})

	_ = m.interceptAskKey("2")
	if m.pendingAsk != nil {
		t.Fatal("answering the only question should clear the ask prompt")
	}
}

func TestSlashCommandsRun(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)

	m.textarea.SetValue("/help")
	m.submit()

	var found bool
	for _, b := range m.transcript {
		if strings.Contains(b.content, "commands") {
			found = true
		}
	}
	if !found {
		t.Fatal("/help output missing from transcript")
	}

	// Unknown commands report instead of running.
	lines, _ := m.handleSlashInput("/nope")
	for _, line := range lines {
		if !strings.Contains(line, "unknown command") {
			t.Fatalf("unknown command line unexpected: %q", line)
		}
	}
}

func TestNewSlashCommandsWithoutEngine(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	for _, name := range []string{"/mcp", "/skills", "/tools", "/status", "/compact", "/effort"} {
		lines, cmd := m.handleSlashInput(name)
		if cmd != nil {
			t.Fatalf("%s should not schedule work without an engine", name)
		}
		if len(lines) == 0 {
			t.Fatalf("%s produced no output", name)
		}
		if !strings.Contains(strings.Join(lines, "\n"), "no engine attached") {
			t.Fatalf("%s output missing guard line: %v", name, lines)
		}
	}
}

func TestEffortValidation(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	lines, _ := m.handleSlashInput("/effort turbo")
	if !strings.Contains(strings.Join(lines, "\n"), "unknown effort") {
		t.Fatalf("bogus effort must be rejected: %v", lines)
	}
}

func TestCostAndDiffSlashCommands(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	lines, _ := m.handleSlashInput("/cost")
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "cost & tokens") {
		t.Fatalf("/cost output missing expected header: %v", lines)
	}

	diffLines, _ := m.handleSlashInput("/diff")
	if len(diffLines) == 0 {
		t.Fatal("/diff output produced no lines")
	}
}

func TestQuitCommandRequestsExit(t *testing.T) {
	m := newTestModel(t)
	// runSlashInput is the real path: it converts quitRequested into a
	// tea.Quit command.
	if cmd := m.runSlashInput("/quit"); cmd == nil {
		t.Fatal("/quit should return a tea.Quit command")
	}
	if !m.quitRequested {
		t.Fatal("quitRequested not set")
	}
}

func TestMenuFilter(t *testing.T) {
	m := newTestModel(t)
	m.openSlashMenu("yo")
	if len(m.menuItems) != 1 || m.menuItems[0].text != "/yolo" {
		t.Fatalf("filter(yo) = %v, want [/yolo]", m.menuItems)
	}
	m.openSlashMenu("")
	if len(m.menuItems) != len(slashCommands) {
		t.Fatal("empty query should list all commands")
	}
}

func TestModelPrefixDoesNotHijackOtherSlashMenus(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	// Typing "/m" is on the way to /mcp; it must open the command menu,
	// not the /model picker. Build the input the way a user would: "/" then "m".
	m.textarea.SetValue("/")
	key := tea.KeyPressMsg{Code: 'm', Text: "m"}
	updated, _ := m.Update(key)
	m = updated.(Model)
	if m.pickerOpen {
		t.Fatal("'/m' must not open the model picker")
	}
	if !m.menuOpen {
		t.Fatal("'/m' should open the slash command menu")
	}

	// "/model " (with trailing space) opens the picker with an empty filter.
	m.textarea.Reset()
	m.menuOpen = false
	m.textarea.SetValue("/model")
	space := tea.KeyPressMsg{Code: ' ', Text: " "}
	updated, _ = m.Update(space)
	m = updated.(Model)
	if !m.pickerOpen {
		t.Fatal("'/model ' should open the picker")
	}
	if m.menuOpen {
		t.Fatal("picker path must close the command menu")
	}
}

func TestToggleReasoning(t *testing.T) {
	m := newTestModel(t)
	m.handleBusEvent(busEv(events.AgentThinkingDelta, map[string]any{"delta": "hmm"}).ev)
	if !m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("reasoning must start expanded")
	}
	m.toggleReasoning()
	if m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("tab should collapse the newest reasoning block")
	}
	m.toggleReasoning()
	if !m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("second tab should expand again")
	}
}

func TestSealedThinkingShowsDuration(t *testing.T) {
	m := resize(newTestModel(t), 100, 30)
	m.handleBusEvent(busEv(events.AgentThinkingDelta, map[string]any{"delta": "reasoning about it"}).ev)
	m.thinkStart = time.Now().Add(-3 * time.Second)
	m.closeStream()
	b := m.transcript[m.cur.reasoning]
	if b.reasoningOpen {
		t.Fatal("reasoning should auto-collapse when the stream seals")
	}
	if b.thinkingDur != 3*time.Second {
		t.Fatalf("thinkingDur = %v, want 3s", b.thinkingDur)
	}
	rendered := m.renderReasoningBlock(m.cur.reasoning)
	if !strings.Contains(rendered, "3s") {
		t.Fatalf("sealed header missing duration: %q", rendered)
	}
}

// TestSealedStreamRendersMarkdownAndCollapsesThinking covers the full stream
// lifecycle: text/thinking stream raw while open, then seal into collapsed
// thinking + markdown-rendered assistant text.
func TestSealedStreamRendersMarkdownAndCollapsesThinking(t *testing.T) {
	m := resize(newTestModel(t), 100, 30)

	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "# Title\n\nSome **bold** text"}).ev)
	m.handleBusEvent(busEv(events.AgentThinkingDelta, map[string]any{"delta": "reasoning about it"}).ev)

	if !m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("reasoning should be expanded while streaming")
	}

	m.closeStream()

	if m.transcript[m.cur.reasoning].reasoningOpen {
		t.Fatal("reasoning should auto-collapse when the stream seals")
	}
	rendered := m.transcript[m.cur.text].rendered
	if rendered == "" {
		t.Fatal("sealed text block has no markdown render cached")
	}
	if !strings.Contains(rendered, "bold") {
		t.Fatalf("markdown render missing text: %q", rendered)
	}

	view := resize(m, 100, 30).View().Content
	if !strings.Contains(view, "rakit") || !strings.Contains(view, "ready") {
		t.Fatal("view missing chrome")
	}
}

func TestModelPickerFilterAndSelection(t *testing.T) {
	m := newTestModel(t)
	// catalogModels lists the current model first; keep that order here.
	m.picker.items = []catalogEntry{
		{id: "prov-a/m2", display: "Alpha/m2"},
		{id: "prov-a/m1", display: "Alpha/m1"},
		{id: "prov-b/m3", display: "Beta/m3"},
	}
	m.picker.current = "prov-a/m2"
	m.picker.open = true

	m.filterModelPicker("")
	if len(m.picker.view) != 3 {
		t.Fatalf("unfiltered view = %d rows, want 3", len(m.picker.view))
	}
	if v := m.picker.view[0]; v.entry.id != "prov-a/m2" || !v.current {
		t.Fatalf("current model must sort first and be marked: %+v", v)
	}

	m.filterModelPicker("m3")
	if len(m.picker.view) != 1 || m.picker.view[0].entry.id != "prov-b/m3" {
		t.Fatalf("filtered view = %+v", m.picker.view)
	}

	// The display form filters too: "beta" matches only the Beta entry.
	m.filterModelPicker("beta")
	if len(m.picker.view) != 1 || m.picker.view[0].entry.id != "prov-b/m3" {
		t.Fatalf("filter by display name = %+v", m.picker.view)
	}

	m.filterModelPicker("")
	m.picker.sel = 2
	if got := m.pickerSelection(); got != "prov-b/m3" {
		t.Fatalf("selection = %q", got)
	}
	m.movePickerSelection(5)
	if m.picker.sel != 2 {
		t.Fatalf("selection must clamp to last, got %d", m.picker.sel)
	}
	m.movePickerSelection(-9)
	if m.picker.sel != 0 {
		t.Fatalf("selection must clamp to first, got %d", m.picker.sel)
	}
}

// TestCatalogDeduplicatesCurrentModel: a session pinned to the bare model
// name ("mimo-v2-6-flash") must match the catalog entry "prov-x/mimo…" —
// the catalog row is marked current instead of adding a duplicate row.
func TestCatalogDeduplicatesCurrentModel(t *testing.T) {
	profiles := []llm.ProviderProfile{{
		ID:             "prov-x",
		Name:           "Kenari",
		Enabled:        true,
		SelectedModels: []string{"mimo-v2-6-flash", "deepseek-v4-1-flash"},
	}}
	entries := buildCatalog(profiles, "mimo-v2-6-flash")
	if len(entries) != 2 {
		t.Fatalf("catalog = %+v, want 2 entries", entries)
	}
	if entries[0].id != "prov-x/mimo-v2-6-flash" {
		t.Fatalf("current model must sort first, got %+v", entries[0])
	}
	if !catalogMatch(entries[0], "mimo-v2-6-flash") {
		t.Fatalf("bare current name must match the catalog entry %q", entries[0].id)
	}
	if entries[0].display != "Kenari/mimo-v2-6-flash" {
		t.Fatalf("display should use the profile name, got %q", entries[0].display)
	}
}

// TestThinkingStreamsCompact: while reasoning streams, the block renders as
// the ✻ header plus the last few lines — never the full dump.
func TestThinkingStreamsCompact(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	for i := 0; i < 20; i++ {
		m.handleBusEvent(busEv(events.AgentThinkingDelta, map[string]any{
			"delta": fmt.Sprintf("reasoning line %d\n", i),
		}).ev)
	}
	rendered := m.renderReasoningBlock(m.cur.reasoning)
	plain := stripAnsi(rendered)
	if !strings.Contains(plain, "20 lines") {
		t.Fatalf("streaming header missing line count: %q", plain)
	}
	if strings.Contains(plain, "reasoning line 0") {
		t.Fatal("streaming render must show only the tail, not early lines")
	}
	if !strings.Contains(plain, "reasoning line 19") {
		t.Fatalf("streaming render missing the latest line: %q", plain)
	}

	// Sealing collapses; tab expands to the full body.
	m.closeStream()
	m.toggleReasoning()
	expanded := stripAnsi(m.renderReasoningBlock(m.cur.reasoning))
	if !strings.Contains(expanded, "reasoning line 0") {
		t.Fatal("tab-expanded block must show the full body")
	}
	if !strings.Contains(expanded, "(tab collapses)") {
		t.Fatalf("expanded header missing hint: %q", expanded)
	}
}

func TestNoticeMsgAppendsLines(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	updated, _ := m.Update(noticeMsg{lines: []string{"one", "two"}})
	m = updated.(Model)
	last := m.transcript[len(m.transcript)-1]
	if last.content != "two" {
		t.Fatalf("notice lines not appended in order, last = %q", last.content)
	}
}

func TestWorkingVerbRotates(t *testing.T) {
	m := newTestModel(t)
	m.tick = 0
	first := m.verb()
	m.tick = 31
	if m.verb() == first {
		t.Fatal("verb should rotate with the tick counter")
	}
}

func TestScrollKeysTrackStickiness(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	if !m.stuckBottom {
		t.Fatal("model must start pinned to the bottom")
	}

	// Feed enough content to overflow the viewport.
	for i := 0; i < 30; i++ {
		m.appendBlock("line")
	}
	m.refreshViewport()

	// pgup unpins; pgdown re-pins; the status bar reflects both states.
	handled, _ := m.interceptKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if !handled || m.stuckBottom {
		t.Fatal("pgup must unpin the transcript")
	}
	if bar := m.renderStatusBar(); !strings.Contains(stripAnsi(bar), "↓ scroll") {
		t.Fatalf("unpinned status bar missing ↓ scroll indicator: %q", stripAnsi(bar))
	}

	handled, _ = m.interceptKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !handled || !m.stuckBottom {
		t.Fatal("pgdown must re-pin the transcript")
	}
	if bar := m.renderStatusBar(); strings.Contains(stripAnsi(bar), "↓ scroll") {
		t.Fatalf("pinned status bar must not show ↓ scroll: %q", stripAnsi(bar))
	}

	// Wheel up unpins, wheel down re-pins.
	m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp, Y: 1})
	if m.stuckBottom {
		t.Fatal("wheel up must unpin")
	}
	m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Y: 1})
	if !m.stuckBottom {
		t.Fatal("wheel down must re-pin")
	}

	// After re-pinning, appended lines keep the viewport on the last row.
	m.appendBlock("tail")
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Fatalf("pinned viewport must sit at the bottom, yoffset=%d", m.viewport.YOffset())
	}
}

// TestMenuOverlayFitsInWindow guards the clipping bug: with the slash menu
// open, the composed view (header + viewport + footer + status) must fit the
// terminal height the layout was given. The menu opens through Update — that
// is where the layout re-syncs for overlay changes.
func TestMenuOverlayFitsInWindow(t *testing.T) {
	w, h := 80, 24
	m := resize(newTestModel(t), w, h)

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updated.(Model)
	if !m.menuOpen {
		t.Fatal("typing '/' should open the slash menu")
	}

	frames := strings.Count(m.View().Content, "\n") + 1
	if frames > h {
		t.Fatalf("view with open menu has %d rows, exceeds terminal height %d", frames, h)
	}

	// The menu must also window itself instead of listing everything.
	if got := len(strings.Split(m.renderSlashMenu(), "\n")); got > menuMaxRows+3 {
		t.Fatalf("menu renders %d rows, cap is %d", got, menuMaxRows+3)
	}
}

// TestPumpCoalescesDeltas: streaming deltas arriving back-to-back coalesce
// into one message per flush, while passthrough events keep their position
// in the stream and different sessions never merge.
func TestPumpCoalescesDeltas(t *testing.T) {
	bus := events.NewBus()
	pmp := &eventPump{} // prog stays nil: flush is a no-op, we read pending directly
	unsubs := []func(){}
	for _, typ := range []events.EventType{
		events.AgentMessageDelta, events.AgentThinkingDelta,
		events.AgentToolStart, events.AgentToolEnd,
	} {
		t := typ
		unsubs = append(unsubs, bus.Subscribe(t, func(ev events.Event) { pmp.push(ev) }))
	}
	defer func() { _ = unsubs }()
	_ = unsubs

	deltas := []string{"a", "b", "c"}
	for _, d := range deltas {
		bus.Publish(events.Event{Type: events.AgentMessageDelta, Data: map[string]any{
			"session_id": "s1", "kind": "text", "delta": d,
		}})
	}
	// A thinking run for another session must not merge into s1's run.
	bus.Publish(events.Event{Type: events.AgentThinkingDelta, Data: map[string]any{
		"session_id": "s2", "delta": "other",
	}})
	// A tool event between runs acts as an ordering barrier.
	bus.Publish(events.Event{Type: events.AgentToolStart, Data: map[string]any{
		"session_id": "s1", "tool_call_id": "t1", "name": "bash",
	}})
	// More text after the barrier forms its own run.
	bus.Publish(events.Event{Type: events.AgentMessageDelta, Data: map[string]any{
		"session_id": "s1", "kind": "text", "delta": "d",
	}})

	var msgs []tea.Msg
	for _, it := range pmp.pending {
		if it.ev != nil {
			msgs = append(msgs, busEvent{ev: *it.ev})
			continue
		}
		msgs = append(msgs, it.typ)
	}
	if len(msgs) != 4 {
		t.Fatalf("pending = %d items, want 4 (text run, thinking run, tool, text run)", len(pmp.pending))
	}
	if pmp.pending[0].typ != events.AgentMessageDelta || pmp.pending[0].text != "abc" {
		t.Fatalf("first run = %q, want abc", pmp.pending[0].text)
	}
	if pmp.pending[1].sid != "s2" || pmp.pending[1].text != "other" {
		t.Fatalf("other-session run = %q (%s)", pmp.pending[1].text, pmp.pending[1].sid)
	}
	if pmp.pending[2].ev == nil || pmp.pending[2].ev.Type != events.AgentToolStart {
		t.Fatalf("tool event missing at index 2: %+v", pmp.pending[2])
	}
	if pmp.pending[3].text != "d" {
		t.Fatalf("post-barrier run = %q, want d", pmp.pending[3].text)
	}
}

// TestActivityLineShowsWhileWaiting: between sending a message and the first
// streamed delta, the transcript carries an inline activity line; it
// disappears once content arrives.
func TestActivityLineShowsWhileWaiting(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	m.busy = true
	m.awaitFirstDelta = true
	m.refreshViewport()
	if !strings.Contains(stripAnsi(m.viewport.View()), "…") {
		t.Fatal("activity line missing while waiting for the first delta")
	}

	m.handleBusEvent(busEv(events.AgentMessageDelta, map[string]any{"kind": "text", "delta": "hello"}).ev)
	m.refreshViewport()
	if m.awaitFirstDelta {
		t.Fatal("first delta must clear the waiting flag")
	}
}
