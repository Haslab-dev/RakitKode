package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/events"
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

func TestNewModelBuildsHeader(t *testing.T) {
	m := newTestModel(t)
	if len(m.transcript) == 0 {
		t.Fatal("expected session header block in transcript")
	}
	if !strings.Contains(m.transcript[0].content, "Rakit") {
		t.Fatalf("header block missing title, got %q", m.transcript[0].content)
	}
}

func TestViewAndResizeWithoutEngine(t *testing.T) {
	m := resize(newTestModel(t), 80, 24)
	if !m.ready {
		t.Fatal("model not ready after WindowSizeMsg")
	}
	view := m.View().Content
	if !strings.Contains(view, "Rakit") {
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
	idx, ok := m.toolLines["t1"]
	if !ok {
		t.Fatal("tool line not tracked")
	}
	if !strings.Contains(m.transcript[idx].content, "✓") {
		t.Fatalf("tool line missing success mark: %q", m.transcript[idx].content)
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
	for _, line := range m.handleSlashInput("/nope") {
		if !strings.Contains(line, "unknown command") {
			t.Fatalf("unknown command line unexpected: %q", line)
		}
	}
}

func TestQuitCommandRequestsExit(t *testing.T) {
	m := newTestModel(t)
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
	if !strings.Contains(view, "Rakit") || !strings.Contains(view, "ready") {
		t.Fatal("view missing chrome")
	}
}

func TestModelPickerFilterAndSelection(t *testing.T) {
	m := newTestModel(t)
	// catalogModels lists the current model first; keep that order here.
	m.picker.items = []string{"prov-a/m2", "prov-a/m1", "prov-b/m3"}
	m.picker.current = "prov-a/m2"
	m.picker.open = true

	m.filterModelPicker("")
	if len(m.picker.view) != 3 {
		t.Fatalf("unfiltered view = %d rows, want 3", len(m.picker.view))
	}
	if m.picker.view[0].model != "prov-a/m2" || m.picker.view[0].mark != "●" {
		t.Fatalf("current model must sort first and be marked: %+v", m.picker.view[0])
	}

	m.filterModelPicker("m3")
	if len(m.picker.view) != 1 || m.picker.view[0].model != "prov-b/m3" {
		t.Fatalf("filtered view = %+v", m.picker.view)
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
