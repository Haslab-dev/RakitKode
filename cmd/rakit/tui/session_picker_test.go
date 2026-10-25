package tui

import (
	"testing"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
)

func TestSessionPickerFilterAndMove(t *testing.T) {
	p := sessionPicker{items: []sessionEntry{
		{id: "1", title: "alpha work"},
		{id: "2", title: "beta fix"},
		{id: "3", title: "beta docs"},
	}}
	p.filter("beta")
	if len(p.view) != 2 {
		t.Fatalf("view = %d, want 2", len(p.view))
	}
	p.move(1)
	if p.sel != 1 {
		t.Fatalf("sel = %d, want 1", p.sel)
	}
	p.filter("nothing")
	if len(p.view) != 0 || p.sel != 0 {
		t.Fatalf("empty filter: view=%d sel=%d", len(p.view), p.sel)
	}
}

func TestOpenAndSwitchSession(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e, err := engine.New(engine.Options{Home: home, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	s1, err := e.Manager().CreateSession("alpha session", agent.RoleCoding, ws)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := e.Manager().CreateSession("beta session", agent.RoleCoding, ws)
	if err != nil {
		t.Fatal(err)
	}

	m := New(e, s1.ID)
	m.openSessionPicker()
	if !m.sessPicker.open {
		t.Fatal("picker did not open")
	}
	if len(m.sessPicker.view) < 2 {
		t.Fatalf("entries = %d, want >= 2", len(m.sessPicker.view))
	}

	m.switchSession(s2.ID)
	if m.sessionID != s2.ID {
		t.Fatalf("session = %q, want %q", m.sessionID, s2.ID)
	}
	if m.sessPicker.open {
		t.Fatal("picker should close after selection")
	}
	if !m.textarea.Focused() {
		t.Fatal("input should be focused after switching sessions")
	}
}
