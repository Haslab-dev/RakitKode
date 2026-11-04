package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	DefaultLoopWarningRepeats = 3
	DefaultLoopBreakerRepeats = 6
	DefaultLoopHistoryWindow  = 12
)

// DecisionAdvisor provides intelligent System-1 reflection and course correction (Pandu-Jev / Pandu-Micro).
type DecisionAdvisor interface {
	SuggestReflection(toolName string, args map[string]any, output string) string
}

// ToolExecutionRecord stores a snapshot of one tool execution for pattern detection.
type ToolExecutionRecord struct {
	ToolName  string
	InputHash string
	OutputLen int
	Success   bool
}

// TurnLoopGuard monitors tool execution sequences during an agent turn to detect
// and halt spinning, cyclic, or repetitive loops. It pairs with Pandu-Jev to deliver
// smart model reflection advice instead of blunt premature termination.
type TurnLoopGuard struct {
	mu             sync.Mutex
	windowSize     int
	warningRepeats int
	breakerRepeats int
	history        []ToolExecutionRecord

	consecutiveTool  string
	consecutiveInput string
	consecutiveCount int
}

// NewTurnLoopGuard initializes a TurnLoopGuard with sensible defaults.
func NewTurnLoopGuard() *TurnLoopGuard {
	return &TurnLoopGuard{
		windowSize:     DefaultLoopHistoryWindow,
		warningRepeats: DefaultLoopWarningRepeats,
		breakerRepeats: DefaultLoopBreakerRepeats,
		history:        make([]ToolExecutionRecord, 0, DefaultLoopHistoryWindow),
	}
}

// Record observes a tool invocation and its result, optionally querying a DecisionAdvisor (Pandu-Jev).
// Returns:
// - isLoop: true if the circuit breaker threshold has been triggered (turn should halt).
// - warningMsg: non-empty if warning threshold was hit and an advisory system reminder should be injected.
func (g *TurnLoopGuard) Record(call ToolCall, res ToolResult, advisor ...DecisionAdvisor) (isLoop bool, warningMsg string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	inputJSON, _ := json.Marshal(call.Input)
	inputHash := hashString(string(inputJSON))

	rec := ToolExecutionRecord{
		ToolName:  call.Name,
		InputHash: inputHash,
		OutputLen: len(res.Content),
		Success:   res.Success,
	}

	// 1. Consecutive identical tool + input check
	if call.Name == g.consecutiveTool && inputHash == g.consecutiveInput {
		g.consecutiveCount++
	} else {
		g.consecutiveTool = call.Name
		g.consecutiveInput = inputHash
		g.consecutiveCount = 1
	}

	// Append to sliding window
	if len(g.history) >= g.windowSize {
		g.history = g.history[1:]
	}
	g.history = append(g.history, rec)

	// Check circuit breaker (hard cap)
	if g.consecutiveCount >= g.breakerRepeats {
		return true, fmt.Sprintf("Circuit breaker tripped: tool %q was invoked %d times with identical input without progress.", call.Name, g.consecutiveCount)
	}

	// Check warning / smart reflection threshold
	if g.consecutiveCount == g.warningRepeats {
		var smartAdvice string
		if len(advisor) > 0 && advisor[0] != nil {
			smartAdvice = advisor[0].SuggestReflection(call.Name, call.Input, res.Content)
		}
		if smartAdvice != "" {
			warningMsg = fmt.Sprintf("💡 Pandu-Jev Smart Decision Reflection for %q:\n%s\n(Notice: tool %q executed %d times consecutively. Pivot to an alternative tool or strategy.)", call.Name, smartAdvice, call.Name, g.consecutiveCount)
		} else {
			warningMsg = fmt.Sprintf("Notice: tool %q has been executed %d times consecutively with identical parameters. Please reconsider your approach or verify if a different tool or strategy is required.", call.Name, g.consecutiveCount)
		}
		return false, warningMsg
	}

	// 2. Cyclic ping-pong check across recent history (e.g. A -> B -> A -> B -> A -> B)
	if len(g.history) >= 6 {
		n := len(g.history)
		if g.history[n-1].ToolName == g.history[n-3].ToolName &&
			g.history[n-3].ToolName == g.history[n-5].ToolName &&
			g.history[n-2].ToolName == g.history[n-4].ToolName &&
			g.history[n-4].ToolName == g.history[n-6].ToolName &&
			!g.history[n-1].Success && !g.history[n-2].Success {
			var smartAdvice string
			if len(advisor) > 0 && advisor[0] != nil {
				smartAdvice = advisor[0].SuggestReflection(g.history[n-1].ToolName, nil, res.Content)
			}
			if smartAdvice != "" {
				return false, fmt.Sprintf("💡 Pandu-Jev Smart Loop Intervention: Cyclic alternating tool execution detected between %q and %q.\nGuidance: %s", g.history[n-2].ToolName, g.history[n-1].ToolName, smartAdvice)
			}
			return false, fmt.Sprintf("Notice: cyclic alternating tool execution detected between %q and %q. Please synthesize findings and adjust your plan.", g.history[n-2].ToolName, g.history[n-1].ToolName)
		}
	}

	return false, ""
}

// Reset clears the loop guard state.
func (g *TurnLoopGuard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.history = g.history[:0]
	g.consecutiveTool = ""
	g.consecutiveInput = ""
	g.consecutiveCount = 0
}

func hashString(s string) string {
	h := sha256.New()
	io.WriteString(h, s)
	return hex.EncodeToString(h.Sum(nil))
}
