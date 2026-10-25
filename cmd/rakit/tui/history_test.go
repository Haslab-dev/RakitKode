package tui

import "testing"

func TestHistoryRecall(t *testing.T) {
	m := New(nil, "s")
	m.recordHistory("first")
	m.recordHistory("second")
	if len(m.history) != 2 {
		t.Fatalf("history = %v", m.history)
	}
	m.textarea.SetValue("draft")
	m.historyPrev()
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("prev = %q", got)
	}
	m.historyPrev()
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("prev2 = %q", got)
	}
	m.historyNext()
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("next = %q", got)
	}
	m.historyNext()
	if got := m.textarea.Value(); got != "draft" {
		t.Fatalf("draft restore = %q", got)
	}
}
