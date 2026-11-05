package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStripCPR(t *testing.T) {
	tests := map[string]string{
		"[46;75R":                   "",
		"\x1b[1;2R":                 "",
		"[46;75Rkk":                 "kk",
		"hello":                     "hello",
		"file[1;2R.txt":             "file.txt",
		"<64;46;24M":                "",
		"\x1b[<64;46;24M":            "",
		"<0;12;34m":                 "",
		"]11;rgb:0000/0000/0000":    "",
		"rgb:0000/0000/0000":        "",
		"000/0000/0000":            "",
		"0000/0000/0000":           "",
		"\x1b]11;rgb:0000/0000/0000\x1b\\": "",
	}
	for in, want := range tests {
		if got := stripCPR(in); got != want {
			t.Fatalf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeKeyPressCPR(t *testing.T) {
	// Pure CPR response with brackets
	msg := tea.KeyPressMsg(tea.Key{Text: "[46;75R", Code: '['})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("pure CPR response should be dropped")
	}

	// Orphan CPR response without leading brackets (e.g. 1R, 24;1R)
	msg = tea.KeyPressMsg(tea.Key{Text: "1R", Code: '1'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("orphan CPR '1R' should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "24;1R", Code: '2'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("orphan CPR '24;1R' should be dropped")
	}

	// SGR mouse motion escape sequence (e.g. <64;46;24M)
	msg = tea.KeyPressMsg(tea.Key{Text: "<64;46;24M", Code: '<'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("SGR mouse sequence '<64;46;24M' should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "\x1b[<0;10;20m", Code: 0x1b})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("SGR mouse sequence with escape should be dropped")
	}

	// OSC 11 color query response (e.g. ]11;rgb:0000/0000/0000 or rgb:0000/0000/0000)
	msg = tea.KeyPressMsg(tea.Key{Text: "]11;rgb:0000/0000/0000", Code: ']'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("OSC 11 sequence ']11;rgb:0000/0000/0000' should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "rgb:0000/0000/0000", Code: 'r'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("OSC 11 color fragment 'rgb:0000/0000/0000' should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "000/0000/0000", Code: '0'})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("OSC 11 color fragment '000/0000/0000' should be dropped")
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "\x1b]11;rgb:ffff/ffff/ffff\x1b\\", Code: 0x1b})
	if _, ok := sanitizeKeyPress(msg); ok {
		t.Fatal("OSC 11 sequence with escape and string terminator should be dropped")
	}

	// Mixed CPR and printable text
	msg = tea.KeyPressMsg(tea.Key{Text: "[46;75Rkk", Code: '['})
	got, ok := sanitizeKeyPress(msg)
	if !ok || got.Text != "kk" {
		t.Fatalf("sanitized = %q ok=%v", got.Text, ok)
	}

	// Regular keys must be preserved
	msg = tea.KeyPressMsg(tea.Key{Text: "regular", Code: 'r'})
	if got, ok := sanitizeKeyPress(msg); !ok || got.Text != "regular" {
		t.Fatalf("regular key changed: %q ok=%v", got.Text, ok)
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "1", Code: '1'})
	if got, ok := sanitizeKeyPress(msg); !ok || got.Text != "1" {
		t.Fatalf("single digit '1' changed: %q ok=%v", got.Text, ok)
	}
	msg = tea.KeyPressMsg(tea.Key{Text: "R", Code: 'R'})
	if got, ok := sanitizeKeyPress(msg); !ok || got.Text != "R" {
		t.Fatalf("single letter 'R' changed: %q ok=%v", got.Text, ok)
	}
}
