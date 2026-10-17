package agent

import (
	"strings"
	"testing"
)

func TestThoughtStreamScanner(t *testing.T) {
	tests := []struct {
		name         string
		chunks       []string
		wantText     string
		wantThinking string
	}{
		{
			name:         "Single chunk with think tag",
			chunks:       []string{"<think>Reasoning carefully here</think>This is the final response."},
			wantText:     "This is the final response.",
			wantThinking: "Reasoning carefully here",
		},
		{
			name: "Split tag across chunks",
			chunks: []string{
				"<th",
				"ink>Part 1 of thought. ",
				"Part 2 of thought.</thi",
				"nk>Now the visible text.",
			},
			wantText:     "Now the visible text.",
			wantThinking: "Part 1 of thought. Part 2 of thought.",
		},
		{
			name: "Thought tag alternate names",
			chunks: []string{
				"<thought>Thinking with thought tag</thought>Text 1.",
				"<thinking>Thinking with thinking tag</thinking>Text 2.",
			},
			wantText:     "Text 1.Text 2.",
			wantThinking: "Thinking with thought tagThinking with thinking tag",
		},
		{
			name:         "Preserves HTML tags",
			chunks:       []string{"<div>Hello <b>world</b></div>"},
			wantText:     "<div>Hello <b>world</b></div>",
			wantThinking: "",
		},
		{
			name:         "No thought tags",
			chunks:       []string{"Just standard text response without any thinking tags."},
			wantText:     "Just standard text response without any thinking tags.",
			wantThinking: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newThoughtStreamScanner()
			var gotText, gotThinking string

			for _, c := range tc.chunks {
				events := s.feed(c)
				for _, ev := range events {
					if ev.kind == "text" {
						gotText += ev.text
					} else if ev.kind == "thinking" {
						gotThinking += ev.text
					}
				}
			}

			for _, ev := range s.flush() {
				if ev.kind == "text" {
					gotText += ev.text
				} else if ev.kind == "thinking" {
					gotThinking += ev.text
				}
			}

			if gotText != tc.wantText {
				t.Errorf("gotText = %q, want %q", gotText, tc.wantText)
			}
			if gotThinking != tc.wantThinking {
				t.Errorf("gotThinking = %q, want %q", gotThinking, tc.wantThinking)
			}
		})
	}
}

// TestFeedDropsOrphanAngleBracket covers gateways that strip reasoning tags
// per chunk: "<" + narration arrives with no closing ">", and the stray "<"
// must not leak into the visible text.
func TestFeedDropsOrphanAngleBracket(t *testing.T) {
	s := newThoughtStreamScanner()
	// split the way a stream would: "<" alone, then the narration
	for _, chunk := range []string{"<", "Only one hit. Let me look at the context and check for case variations.\n\n"} {
		for _, ev := range s.feed(chunk) {
			if strings.HasPrefix(ev.text, "<") {
				t.Fatalf("orphan < leaked as text: %q", ev.text)
			}
			t.Logf("event %s: %q", ev.kind, ev.text)
		}
	}
	rest := s.flush()
	for _, ev := range rest {
		if strings.HasPrefix(ev.text, "<") {
			t.Fatalf("orphan < leaked at flush: %q", ev.text)
		}
	}
}

// TestFeedKeepsLegitProse ensures normal text containing "<" (comparisons,
// generics) still survives.
func TestFeedKeepsLegitProse(t *testing.T) {
	s := newThoughtStreamScanner()
	var text strings.Builder
	for _, chunk := range []string{"use x < 10 as the bound, then map[T] ", "works"} {
		for _, ev := range s.feed(chunk) {
			if ev.kind == "text" {
				text.WriteString(ev.text)
			}
		}
	}
	for _, ev := range s.flush() {
		if ev.kind == "text" {
			text.WriteString(ev.text)
		}
	}
	if !strings.Contains(text.String(), "x < 10") {
		t.Fatalf("legit comparison lost: %q", text.String())
	}
}
