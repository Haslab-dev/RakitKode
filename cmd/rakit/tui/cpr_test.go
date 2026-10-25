package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStripCPR(t *testing.T) {
	tests := map[string]string{
		"[46;75R":       "",
		"\x1b[1;2R":     "",
		"[46;75Rkk":     "kk",
		"hello":         "hello",
		"file[1;2R.txt": "file.txt",
	}
	for in, want := range tests {
		if got := stripCPR(in); got != want {
			t.Fatalf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeKeyPressCPR(t *testing.T) {
	msg := tea.KeyPressMsg(tea.Key{Text: "[46;75R", Code: '['})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("pure CPR response should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "[46;75Rkk", Code: '['})
	got, ok := sanitizeKeyPress(msg)
	if !ok || got.Text != "kk" {
		t.Fatalf("sanitized = %q ok=%v", got.Text, ok)
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "regular", Code: 'r'})
	if got, ok := sanitizeKeyPress(msg); !ok || got.Text != "regular" {
		t.Fatalf("regular key changed: %q ok=%v", got.Text, ok)
	}
}
