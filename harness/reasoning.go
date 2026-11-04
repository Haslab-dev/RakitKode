package harness

import (
	"strings"
	"time"
)

// StreamReasoningSplitter splits an incoming text delta stream into user-visible
// text deltas and model reasoning deltas (DeepSeek-R1, Claude 3.7, and o-series models).
// It detects and handles in-band reasoning tags (<think>, <thought>, <thinking>, <scratchpad>)
// even when they arrive split across network packet chunks.
type StreamReasoningSplitter struct {
	inThinking  bool
	buf         strings.Builder
	onText      func(delta string)
	onReasoning func(delta string)

	// Metrics
	startedAt        time.Time
	reasoningChars   int
	userVisibleChars int
}

// NewStreamReasoningSplitter constructs a new dual-channel streaming splitter.
func NewStreamReasoningSplitter(onText func(string), onReasoning func(string)) *StreamReasoningSplitter {
	return &StreamReasoningSplitter{
		onText:      onText,
		onReasoning: onReasoning,
		startedAt:   time.Now(),
	}
}

// Feed consumes a new incoming stream delta.
func (s *StreamReasoningSplitter) Feed(chunk string) {
	if chunk == "" {
		return
	}
	s.buf.WriteString(chunk)
	s.processBuffer(false)
}

// Flush completes the stream, emitting any trailing buffered content.
func (s *StreamReasoningSplitter) Flush() {
	s.processBuffer(true)
	rem := s.buf.String()
	s.buf.Reset()
	if rem != "" {
		if s.inThinking {
			s.reasoningChars += len(rem)
			if s.onReasoning != nil {
				s.onReasoning(rem)
			}
		} else {
			s.userVisibleChars += len(rem)
			if s.onText != nil {
				s.onText(rem)
			}
		}
	}
	s.inThinking = false
}

// ReasoningChars returns the count of reasoning characters parsed.
func (s *StreamReasoningSplitter) ReasoningChars() int {
	return s.reasoningChars
}

// UserVisibleChars returns the count of final user-facing text characters parsed.
func (s *StreamReasoningSplitter) UserVisibleChars() int {
	return s.userVisibleChars
}

func (s *StreamReasoningSplitter) processBuffer(final bool) {
	for s.buf.Len() > 0 {
		buf := s.buf.String()

		if !s.inThinking {
			idx := strings.Index(buf, "<")
			if idx < 0 {
				// No tag start found: all is user text
				s.emitText(buf)
				s.buf.Reset()
				return
			}
			if idx > 0 {
				s.emitText(buf[:idx])
				s.buf.Reset()
				s.buf.WriteString(buf[idx:])
				buf = s.buf.String()
			}

			// Starts with '<'
			gtIdx := strings.Index(buf, ">")
			if gtIdx < 0 {
				if final {
					s.emitText(buf)
					s.buf.Reset()
				}
				// Wait for closing '>' in subsequent chunk
				return
			}

			tag := buf[1:gtIdx]
			tagClean := strings.ToLower(strings.TrimSpace(tag))
			if isThinkingOpenTag(tagClean) {
				s.inThinking = true
				s.buf.Reset()
				s.buf.WriteString(buf[gtIdx+1:])
			} else {
				// Regular text or unrelated tag (e.g., comparison or HTML tag)
				s.emitText(buf[:gtIdx+1])
				s.buf.Reset()
				s.buf.WriteString(buf[gtIdx+1:])
			}
		} else {
			// Inside thinking block: look for closing tag </think>, </thought>, etc.
			idx := strings.Index(buf, "</")
			if idx < 0 {
				// Check if there is a trailing '<' at the end of the buffer
				if strings.HasSuffix(buf, "<") && !final {
					if len(buf) > 1 {
						s.emitReasoning(buf[:len(buf)-1])
						s.buf.Reset()
						s.buf.WriteString("<")
					}
					return
				}
				s.emitReasoning(buf)
				s.buf.Reset()
				return
			}
			if idx > 0 {
				s.emitReasoning(buf[:idx])
				s.buf.Reset()
				s.buf.WriteString(buf[idx:])
				buf = s.buf.String()
			}

			// Starts with '</'
			gtIdx := strings.Index(buf, ">")
			if gtIdx < 0 {
				if final {
					s.emitReasoning(buf)
					s.buf.Reset()
				}
				// Wait for '>'
				return
			}

			tag := buf[2:gtIdx]
			tagClean := strings.ToLower(strings.TrimSpace(tag))
			if isThinkingCloseTag(tagClean) {
				s.inThinking = false
				s.buf.Reset()
				s.buf.WriteString(buf[gtIdx+1:])
			} else {
				// Not a closing thought tag, still inside thinking
				s.emitReasoning(buf[:gtIdx+1])
				s.buf.Reset()
				s.buf.WriteString(buf[gtIdx+1:])
			}
		}
	}
}

func (s *StreamReasoningSplitter) emitText(text string) {
	if text == "" {
		return
	}
	s.userVisibleChars += len(text)
	if s.onText != nil {
		s.onText(text)
	}
}

func (s *StreamReasoningSplitter) emitReasoning(reasoning string) {
	if reasoning == "" {
		return
	}
	s.reasoningChars += len(reasoning)
	if s.onReasoning != nil {
		s.onReasoning(reasoning)
	}
}

func isThinkingOpenTag(t string) bool {
	return t == "think" || t == "thought" || t == "thinking" || t == "scratchpad" || t == "reasoning"
}

func isThinkingCloseTag(t string) bool {
	return t == "think" || t == "thought" || t == "thinking" || t == "scratchpad" || t == "reasoning"
}
