package tui

import "testing"

func TestBlockCursorNavigationAndToggle(t *testing.T) {
	m := Model{blockCursor: -1, toolLines: map[string]int{}}
	m.transcript = []block{
		{kind: blockPlain},
		{kind: blockReasoning, reasoningOpen: false},
		{kind: blockText},
		{kind: blockTool, tool: &toolInfo{name: "read", state: toolOK}},
	}

	m.moveBlockCursor(-1) // newest expandable
	if !m.blockCursorActive || m.blockCursor != 3 {
		t.Fatalf("cursor = %d active=%v, want 3", m.blockCursor, m.blockCursorActive)
	}
	m.moveBlockCursor(-1)
	if m.blockCursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.blockCursor)
	}
	m.toggleReasoning()
	if !m.transcript[1].reasoningOpen {
		t.Fatal("reasoning should be open")
	}
	m.moveBlockCursor(1)
	if m.blockCursor != 3 {
		t.Fatalf("cursor = %d, want 3", m.blockCursor)
	}
	m.toggleToolOutput()
	if !m.transcript[3].tool.expanded {
		t.Fatal("tool should be expanded")
	}
}
