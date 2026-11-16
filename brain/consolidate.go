package brain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ToolTrace is one tool execution observed during a turn.
type ToolTrace struct {
	Name    string
	Args    map[string]any
	IsError bool
	Output  string // truncated output or error text
}

// TurnRecord is the neutral, engine-agnostic view of one finished turn.
// The agent package builds it from its session transcript; brain never
// imports the agent package (that would be an import cycle).
type TurnRecord struct {
	SessionID      string
	TurnID         string // stable id used as fact source, e.g. "<session>:<n>"
	UserText       string // the user prompt that started the turn
	AssistantText  string // assistant prose produced during the turn
	ToolTrace      []ToolTrace
	Files          []string // file paths touched by mutating tools
	Corrections    []string // assistant/user text that followed tool failures
	Objective      string   // session progress goal, if any
	CompletedSteps []string
	FinalSummary   string // last assistant text of the turn (outcome)
	CompletedAt    time.Time
}

// decisionRe matches assistant prose lines that record a decision.
var decisionRe = regexp.MustCompile(`(?i)\b(decided to|going with|switched to|will use|instead of)\b`)

// becauseRe pulls the causal clause out of a correction so pitfall facts read
// "X fails because Y" instead of dumping the whole message.
var becauseRe = regexp.MustCompile(`(?i)[^.!\n]*\bbecause\b[^.!\n]*`)

// codeishRe filters lines that are code, not prose (code-derivable facts must
// not enter the fact log — the code itself is the source of truth).
var codeishRe = regexp.MustCompile("^(```|\\s{4,}|\\t|[<>*#-]{1,2}\\s*$)")

// ConsolidateTurn deterministically mines a finished turn into facts and a
// session digest. It is pure rule-based extraction — no LLM call, no file
// contents, nothing derivable from re-reading the code — and it is idempotent
// per turn: repeated runs produce identical hashes and dedupe to no-ops.
func ConsolidateTurn(b *Brain, rec TurnRecord) error {
	if b == nil {
		return fmt.Errorf("brain: nil brain")
	}
	source := rec.TurnID
	if source == "" {
		source = "consolidation:" + rec.CompletedAt.UTC().Format(time.RFC3339)
	}

	var appended int

	// Decisions: assistant prose lines that record a choice. Lines inside
	// fenced code blocks are skipped entirely — code is the source of truth
	// and must not enter the fact log as prose facts.
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(rec.AssistantText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !decisionRe.MatchString(trimmed) || codeishRe.MatchString(trimmed) {
			continue
		}
		line := cleanFactLine(trimmed)
		if len(line) < 8 || seen[strings.ToLower(line)] {
			continue
		}
		seen[strings.ToLower(line)] = true
		if _, err := b.AppendFact(KindDecision, line, rec.Files, source); err == nil {
			appended++
		}
	}

	// Pitfalls: tool failures paired with the correction that followed.
	pitfalls := extractPitfalls(rec)
	for _, p := range pitfalls {
		if _, err := b.AppendFact(KindPitfall, p, rec.Files, source); err == nil {
			appended++
		}
	}

	// Digest: the per-session rolling record of objective, requests,
	// decisions, files, and outcome.
	if rec.SessionID != "" {
		if err := b.WriteDigest(rec.SessionID, renderDigest(b, rec)); err != nil {
			return fmt.Errorf("brain: write digest: %w", err)
		}
	}
	return nil
}

// extractPitfalls pairs each failed tool call with the first useful
// correction sentence that followed it.
func extractPitfalls(rec TurnRecord) []string {
	var out []string
	seen := map[string]bool{}
	for _, tr := range rec.ToolTrace {
		if !tr.IsError {
			continue
		}
		correction := pickCorrection(rec.Corrections)
		if correction == "" {
			continue
		}
		text := fmt.Sprintf("%s fails: %s", tr.Name, correction)
		text = cleanFactLine(text)
		if len(text) < 12 {
			continue
		}
		key := strings.ToLower(text)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, text)
	}
	return out
}

// pickCorrection returns the most "causal" sentence from the correction text:
// a "because ..." clause when present, else the first sentence.
func pickCorrection(corrections []string) string {
	joined := strings.Join(corrections, " ")
	if strings.TrimSpace(joined) == "" {
		return ""
	}
	if m := becauseRe.FindString(joined); strings.TrimSpace(m) != "" {
		return strings.TrimSpace(m)
	}
	sentences := strings.FieldsFunc(joined, func(r rune) bool { return r == '.' || r == '!' || r == '\n' })
	if len(sentences) > 0 {
		return strings.TrimSpace(sentences[0])
	}
	return ""
}

// cleanFactLine trims markdown/quote noise and bounds the length.
func cleanFactLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimPrefix(line, "> ")
	for _, p := range []string{"**", "`", "•"} {
		line = strings.ReplaceAll(line, p, "")
	}
	line = strings.Join(strings.Fields(line), " ")
	return truncateRunes(line, MaxFactChars)
}

// renderDigest produces the per-session digest markdown.
func renderDigest(b *Brain, rec TurnRecord) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Session digest — %s\n\n", rec.CompletedAt.UTC().Format("2006-01-02 15:04 MST"))

	sb.WriteString("## Objective\n")
	if obj := strings.TrimSpace(rec.Objective); obj != "" {
		sb.WriteString(obj + "\n")
	} else if req := strings.TrimSpace(rec.UserText); req != "" {
		sb.WriteString(truncateRunes(firstLine(req), 200) + "\n")
	} else {
		sb.WriteString("(none)\n")
	}

	sb.WriteString("\n## Latest request\n")
	if req := strings.TrimSpace(rec.UserText); req != "" {
		sb.WriteString(truncateRunes(firstLine(req), 280) + "\n")
	} else {
		sb.WriteString("(none)\n")
	}

	sb.WriteString("\n## Decisions this turn\n")
	wrote := false
	for _, f := range b.ValidFacts() {
		if f.Kind != KindDecision || f.Source != rec.TurnID {
			continue
		}
		fmt.Fprintf(&sb, "- %s (%s)\n", f.Text, f.Date())
		wrote = true
	}
	if !wrote {
		sb.WriteString("(none recorded)\n")
	}

	sb.WriteString("\n## Files touched\n")
	if len(rec.Files) == 0 {
		sb.WriteString("(none)\n")
	} else {
		files := append([]string(nil), rec.Files...)
		sort.Strings(files)
		limit := len(files)
		if limit > 15 {
			limit = 15
		}
		for _, f := range files[:limit] {
			fmt.Fprintf(&sb, "- `%s`\n", f)
		}
		if len(files) > limit {
			fmt.Fprintf(&sb, "- … (%d more)\n", len(files)-limit)
		}
	}

	sb.WriteString("\n## Outcome\n")
	if out := strings.TrimSpace(rec.FinalSummary); out != "" {
		sb.WriteString(truncateRunes(out, 1200) + "\n")
	} else {
		sb.WriteString("(no assistant summary)\n")
	}
	return sb.String()
}
