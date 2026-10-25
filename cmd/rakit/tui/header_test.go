package tui

import (
	"testing"

	"github.com/Haslab-dev/RakitKode/events"
)

func TestBusyHeaderHidesVerb(t *testing.T) {
	m := New(nil, "s")
	m.busy = true
	if got := m.statusLabel(); got != "" {
		t.Fatalf("busy header should be empty, got %q", got)
	}
	m.busy = false
	m.status = "ready"
	if got := m.statusLabel(); got != "ready" {
		t.Fatalf("idle header = %q", got)
	}
}

func TestSingleReasoningBlockPerStream(t *testing.T) {
	m := New(nil, "s")
	m.width, m.height, m.ready = 100, 30, true
	m.handleBusEvent(events.Event{Type: events.AgentTurnStart, Data: map[string]any{"session_id": "s"}})
	m.handleBusEvent(events.Event{Type: events.AgentThinkingDelta, Data: map[string]any{"session_id": "s", "delta": "a"}})
	m.handleBusEvent(events.Event{Type: events.AgentThinkingDelta, Data: map[string]any{"session_id": "s", "delta": "b"}})
	m.handleBusEvent(events.Event{Type: events.AgentMessageDelta, Data: map[string]any{"session_id": "s", "kind": "text", "delta": "A"}})

	reasoning := 0
	for _, b := range m.transcript {
		if b.kind == blockReasoning {
			reasoning++
		}
	}
	if reasoning != 1 {
		t.Fatalf("reasoning blocks = %d, want 1", reasoning)
	}
}
