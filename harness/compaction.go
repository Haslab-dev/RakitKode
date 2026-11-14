package harness

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DefaultMaxToolOutputLines = 150
	DefaultMaxToolOutputBytes = 32 * 1024 // 32 KB
	DefaultHeadLines          = 25
	DefaultTailLines          = 20
)

// SmartTruncateToolOutput truncates large tool outputs (e.g. huge file reads,
// extensive build logs, verbose test suites) while retaining both the critical
// beginning (head) and error trace/summary (tail).
func SmartTruncateToolOutput(content string, maxLines, maxBytes int) string {
	if maxLines <= 0 {
		maxLines = DefaultMaxToolOutputLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxToolOutputBytes
	}

	lines := strings.Split(content, "\n")
	byteLen := len(content)

	// If within limits, return untouched
	if len(lines) <= maxLines && byteLen <= maxBytes {
		return content
	}

	headCount := DefaultHeadLines
	tailCount := DefaultTailLines
	if headCount+tailCount >= len(lines) {
		// Output is few lines but massive byte size (e.g. minified single-line code or base64)
		if byteLen > maxBytes {
			half := maxBytes / 2
			return fmt.Sprintf("%s\n\n[... %d bytes omitted for context efficiency ...]\n\n%s",
				content[:half], byteLen-maxBytes, content[byteLen-half:])
		}
		return content
	}

	headPart := strings.Join(lines[:headCount], "\n")
	tailPart := strings.Join(lines[len(lines)-tailCount:], "\n")
	omittedLines := len(lines) - (headCount + tailCount)
	omittedBytes := byteLen - (len(headPart) + len(tailPart))
	if omittedBytes < 0 {
		omittedBytes = 0
	}

	return fmt.Sprintf("%s\n\n[... %d lines (%d bytes) omitted for context efficiency. Use targeted commands to view specific sections ...]\n\n%s",
		headPart, omittedLines, omittedBytes, tailPart)
}

// SemanticSummarizeToolResult creates an information-dense, lightweight summary of an older tool result
// for microcompaction (Claude Code / OpenCode style). Unlike blank clearing, it keeps spatial awareness
// of which files exist, which paths were modified, and which commands succeeded.
func SemanticSummarizeToolResult(toolName string, input map[string]any, output string, success bool) string {
	statusStr := "succeeded"
	if !success {
		statusStr = "failed"
	}

	tName := strings.ToLower(toolName)
	switch {
	case strings.Contains(tName, "read") || tName == "cat":
		target := extractPath(input)
		lineCount := countLines(output)
		if target != "" {
			return fmt.Sprintf("[Compacted Tool Result: read %q (%d lines, %s)]", target, lineCount, statusStr)
		}
		return fmt.Sprintf("[Compacted Tool Result: read (%d lines, %s)]", lineCount, statusStr)

	case strings.Contains(tName, "write"):
		target := extractPath(input)
		if target != "" {
			return fmt.Sprintf("[Compacted Tool Result: write %q (%s)]", target, statusStr)
		}
		return fmt.Sprintf("[Compacted Tool Result: write (%s)]", statusStr)

	case strings.Contains(tName, "edit") || strings.Contains(tName, "replace"):
		target := extractPath(input)
		if target != "" {
			return fmt.Sprintf("[Compacted Tool Result: edit %q (%s)]", target, statusStr)
		}
		return fmt.Sprintf("[Compacted Tool Result: edit (%s)]", statusStr)

	case strings.Contains(tName, "bash") || strings.Contains(tName, "shell") || strings.Contains(tName, "exec"):
		cmd := extractCommand(input)
		lineCount := countLines(output)
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		if cmd != "" {
			return fmt.Sprintf("[Compacted Tool Result: bash %q (%d lines output, %s)]", cmd, lineCount, statusStr)
		}
		return fmt.Sprintf("[Compacted Tool Result: bash (%s)]", statusStr)

	case strings.Contains(tName, "search") || strings.Contains(tName, "grep") || strings.Contains(tName, "find"):
		q := extractQuery(input)
		if q != "" {
			return fmt.Sprintf("[Compacted Tool Result: search %q (%s)]", q, statusStr)
		}
		return fmt.Sprintf("[Compacted Tool Result: search (%s)]", statusStr)

	default:
		return fmt.Sprintf("[Compacted Tool Result: %s (%s)]", toolName, statusStr)
	}
}

func extractPath(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"path", "file_path", "target_file", "file", "target"} {
		if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractCommand(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "script"} {
		if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractQuery(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"query", "pattern", "q", "search_term"} {
		if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

var thinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

// PruneReasoning removes verbose <think>...</think> chains from older assistant messages.
// This preserves the exact answer and tool calls while reclaiming reasoning token overhead.
func PruneReasoning(content string) (string, bool) {
	if !strings.Contains(content, "<think>") {
		return content, false
	}
	cleaned := thinkTagRegex.ReplaceAllString(content, "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, cleaned != content
}

// CompactionDecisionSet tracks message indices that have already been compacted
// to enforce monotonic boundaries and guarantee byte stability for prompt caching.
type CompactionDecisionSet struct {
	compacted map[int]string
}

func NewCompactionDecisionSet() *CompactionDecisionSet {
	return &CompactionDecisionSet{compacted: make(map[int]string)}
}

func (s *CompactionDecisionSet) IsCompacted(idx int) bool {
	if s == nil || s.compacted == nil {
		return false
	}
	_, ok := s.compacted[idx]
	return ok
}

func (s *CompactionDecisionSet) Record(idx int, action string) {
	if s != nil {
		if s.compacted == nil {
			s.compacted = make(map[int]string)
		}
		s.compacted[idx] = action
	}
}
