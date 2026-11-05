package harness

import (
	"context"
	"fmt"
	"strings"

	"github.com/Haslab-dev/RakitKode/llm"
)

// StreamHandler receives UI-bound streaming events.
type StreamHandler interface {
	OnTextDelta(delta string)
	OnReasoningDelta(delta string)
	OnToolStatus(callID, toolName, status, detail string)
	OnTurnComplete(resultType TurnResultType, response string)
}

// StepUsageStats carries token consumption and cache hit metrics for a single model step.
type StepUsageStats struct {
	Step                  int     `json:"step"`
	Model                 string  `json:"model"`
	PromptTokens          int     `json:"prompt_tokens"`
	CompletionTokens      int     `json:"completion_tokens"`
	CachedTokens          int     `json:"cached_tokens"`
	PromptCacheHitTokens  int     `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int     `json:"prompt_cache_miss_tokens"`
	CacheHitRate          float64 `json:"cache_hit_rate"`
	TotalTokens           int     `json:"total_tokens"`
	EstimatedCost         float64 `json:"estimated_cost"`
	SavingsCost           float64 `json:"savings_cost"`
	SavingsPct            float64 `json:"savings_pct"`
}

// StepStatsHandler optionally receives per-step token usage and cache metrics.
type StepStatsHandler interface {
	OnStepStats(stats StepUsageStats)
}

// Runtime is the agentic turn loop: port of runtime/methods/turn-loop.ts +
// turn-model-step.ts + turn-tools.ts, simplified to the Go single-process
// model while preserving the machine transitions, tool batching, permission
// gating, compaction thresholds, and turn-control semantics.
type Runtime struct {
	LLM       *llm.LLMClient
	Registry  *ToolRegistry
	Executor  *Executor
	History   *MessageHistory
	LoopGuard *TurnLoopGuard
	Advisor   DecisionAdvisor // Pandu-Jev System-1 smart model reflection & routing
	System    string
	Stream    StreamHandler
	// Compaction thresholds (spec §2.4)
	ContextWindow     int64 // default 200000
	AutoCompactBuffer int64 // 13000
	PreflightReserve  int64 // 21000
	// MaxModelSteps caps runaway loops in the native port (the source relies
	// on no-tool-call exit; we keep an explicit circuit breaker).
	MaxModelSteps int

	usageTokens           int64
	lastPromptTokens      int64
	lastCachedTokens      int64
	totalPromptTokens     int64
	totalCompletionTokens int64
	totalCachedTokens     int64
}

func NewRuntime(llmClient *llm.LLMClient, reg *ToolRegistry, perms *PermissionService, stream StreamHandler) *Runtime {
	return &Runtime{
		LLM:           llmClient,
		Registry:      reg,
		Executor:      &Executor{Registry: reg, Permissions: perms},
		History:       NewMessageHistory(),
		LoopGuard:     NewTurnLoopGuard(),
		ContextWindow: 200000, AutoCompactBuffer: 13000, PreflightReserve: 21000,
		MaxModelSteps: 100,
		Stream:        stream,
	}
}

// SessionContext for the current turn (modes/rules).
type TurnContext struct {
	Session *SessionContext
	TurnNo  int
}

// ExecuteTurn runs one full user turn: the agentic loop until the model
// produces no tool calls, the turn is stopped, or a circuit breaker trips.
func (r *Runtime) ExecuteTurn(ctx context.Context, userContent string, tctx TurnContext) (TurnResultType, string) {
	machine := NewTurnMachine(tctx.Session.SessionID, tctx.TurnNo, userContent)
	_ = machine.Start()
	r.History.AddUser(userContent, "real_user")

	r.usageTokens = r.History.TokenEstimate()
	modelSteps := 0
	for {
		if err := ctx.Err(); err != nil {
			machine.Complete("", ResultCancelled)
			r.emitComplete(ResultCancelled)
			return ResultCancelled, ""
		}
		if modelSteps >= r.MaxModelSteps {
			machine.Complete("", ResultErrorMaxTurns)
			r.emitComplete(ResultErrorMaxTurns)
			return ResultErrorMaxTurns, ""
		}
		// Mid-turn auto/micro compaction check (spec §2.4).
		r.maybeCompact()

		if err := machine.StartModelRequest(""); err != nil {
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}
		// Cancellable model request.
		mctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Dual-channel reasoning splitter: catches <think>...</think> in content deltas
		// and routes reasoning to OnReasoningDelta while keeping OnTextDelta clean.
		splitter := NewStreamReasoningSplitter(
			func(textDelta string) {
				_ = machine.AddStreamingContent(textDelta)
				if r.Stream != nil {
					r.Stream.OnTextDelta(textDelta)
				}
			},
			func(reasoningDelta string) {
				if r.Stream != nil {
					r.Stream.OnReasoningDelta(reasoningDelta)
				}
			},
		)

		resp, err := r.LLM.ChatWithStreamDetailed(mctx, r.providerMessages(), r.providerTools(),
			func(deltaContent, deltaReasoning string) {
				if deltaReasoning != "" && r.Stream != nil {
					r.Stream.OnReasoningDelta(deltaReasoning)
				}
				if deltaContent != "" {
					splitter.Feed(deltaContent)
				}
			}, nil)
		splitter.Flush()

		if err != nil {
			cancel()
			if ctx.Err() != nil {
				// cancellation is a normal result: commit partial text
				if machine.State().StreamingContent != "" {
					r.History.AddAssistant(machine.State().StreamingContent, nil, "", 0)
				}
				machine.Complete(machine.State().StreamingContent, ResultCancelled)
				r.emitComplete(ResultCancelled)
				return ResultCancelled, machine.State().StreamingContent
			}
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}
		modelSteps++
		r.recordUsage(resp, modelSteps)

		if len(resp.ToolCalls) == 0 {
			// Output-token continuation: finishReason length with no tools
			// appends a continuation entry and re-requests (bounded).
			if resp.FinishReason == "length" && modelSteps < r.MaxModelSteps {
				r.History.AddAssistant(resp.Content, nil, "", 0)
				r.History.AddUser("[continue]", "legacy_synthetic")
				continue
			}
			r.History.AddAssistant(resp.Content, nil, "", 0)
			if err := machine.Complete(resp.Content, ResultSuccess); err != nil {
				machine.Fail(err)
			}
			r.emitComplete(ResultSuccess)
			return ResultSuccess, resp.Content
		}

		// Commit assistant tool_use to history with safe repaired JSON arguments.
		calls := make([]MessageToolCall, 0, len(resp.ToolCalls))
		coreCalls := make([]ToolCall, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			calls = append(calls, MessageToolCall{ID: tc.ID, Name: tc.Function.Name, ArgsJSON: tc.Function.Arguments})
			input := map[string]any{}
			_ = SafeUnmarshalToolArgs(tc.Function.Arguments, &input)
			coreCalls = append(coreCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
		}
		r.History.AddAssistant(resp.Content, calls, "", 0)
		if err := machine.ScheduleTools(coreCalls); err != nil {
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}
		if err := machine.StartToolExecution(); err != nil {
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}

		sched, err := ScheduleTools(r.Registry, coreCalls, 10)
		if err != nil {
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}
		var firstStop TurnResultType
		r.Executor.ExecuteSchedule(ctx, sched, tctx.Session, func(res ToolResult) {
			_ = machine.CompleteTool(res.CallID, res)
			status := "completed"
			if !res.Success {
				status = "failed"
			}
			name := ""
			var matchedCall ToolCall
			for _, c := range coreCalls {
				if c.ID == res.CallID {
					name = c.Name
					matchedCall = c
					break
				}
			}
			if r.Stream != nil {
				r.Stream.OnToolStatus(res.CallID, name, status, res.Content)
			}

			// Smart output truncation: keeps head & tail for context efficiency
			truncatedContent := SmartTruncateToolOutput(res.Content, 0, 0)
			r.History.AddToolResult(res.CallID, name, truncatedContent, res.Success)

			// Antiloop & Stall Guard (OpenCode & Pandu-Jev smart decisions)
			if r.LoopGuard != nil {
				isBreaker, warning := r.LoopGuard.Record(matchedCall, res, r.Advisor)
				if isBreaker {
					firstStop = ResultErrorMaxToolCalls
				} else if warning != "" {
					r.History.AddAttachment(warning, "pandu_jev_loop_intervention", false)
				}
			}

			if res.StopTurn && firstStop == "" {
				firstStop = ResultSuccess
			}
		})
		if err := machine.AggregateResults(); err != nil {
			machine.Fail(err)
			r.emitComplete(ResultErrorExecution)
			return ResultErrorExecution, ""
		}
		if firstStop != "" {
			// turnControl.stopTurnAfterResult: complete the turn after the
			// batch, without another model step.
			machine.Complete("", firstStop)
			r.emitComplete(firstStop)
			return firstStop, machine.State().FinalResponse
		}
		// aggregating_results -> awaiting_model_response (the agentic loop)
	}
}

func (r *Runtime) emitComplete(rt TurnResultType) {
	if r.Stream != nil {
		r.Stream.OnTurnComplete(rt, "")
	}
}

func (r *Runtime) maybeCompact() {
	threshold := r.ContextWindow - r.PreflightReserve - r.AutoCompactBuffer
	if r.usageTokens >= threshold {
		// Tier 1: Microcompact older tool outputs monotonically
		r.microcompact()
		if r.usageTokens >= threshold {
			// Tier 2: Prune verbose thinking chains from older turns outside the protection zone
			r.pruneReasoning()
			if r.usageTokens >= threshold {
				// Tier 3: Macrocompact early conversation with summary
				r.macrocompact()
			}
		}
	}
}

// microcompact summarizes older tool results into semantic digests instead of
// blanking them completely (Claude Code / OpenCode parity).
// Compaction is monotonic: once an entry is summarized, its content is byte-stable.
func (r *Runtime) microcompact() {
	const keepRecent = 5
	entries := r.History.Entries()
	toolIdx := []int{}
	for i, e := range entries {
		if e.Kind == EntryKindMessage && e.Message != nil && e.Message.Role == RoleTool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keepRecent {
		return
	}
	changed := false
	for _, i := range toolIdx[:len(toolIdx)-keepRecent] {
		m := entries[i].Message
		if strings.HasPrefix(m.Content, "[Compacted Tool Result:") {
			continue // Already compacted: preserve byte stability for prompt cache prefix
		}
		entries[i].Message.Content = SemanticSummarizeToolResult(m.ToolName, nil, m.Content, !m.IsError)
		changed = true
	}
	if changed {
		r.History.ReplaceMessages(entries)
		r.usageTokens = r.History.TokenEstimate()
	}
}

// pruneReasoning strips verbose <think>...</think> blocks from older assistant turns,
// preserving tool calls and final text while reclaiming context for prompt prefix caching.
func (r *Runtime) pruneReasoning() {
	const keepRecent = 4
	entries := r.History.Entries()
	asstIdx := []int{}
	for i, e := range entries {
		if e.Kind == EntryKindMessage && e.Message != nil && e.Message.Role == RoleAssistant {
			asstIdx = append(asstIdx, i)
		}
	}
	if len(asstIdx) <= keepRecent {
		return
	}
	changed := false
	for _, i := range asstIdx[:len(asstIdx)-keepRecent] {
		m := entries[i].Message
		if cleaned, ok := PruneReasoning(m.Content); ok {
			entries[i].Message.Content = cleaned
			changed = true
		}
	}
	if changed {
		r.History.ReplaceMessages(entries)
		r.usageTokens = r.History.TokenEstimate()
	}
}

// macrocompact summarizes older conversation history when microcompaction is
// insufficient, preserving the root request, a compact summary, and recent turns.
func (r *Runtime) macrocompact() {
	entries := r.History.Entries()
	if len(entries) <= 6 {
		return
	}
	const keepTail = 4
	first := entries[0]
	tail := entries[len(entries)-keepTail:]

	summaryContent := "<context-summary>\n### Prior Conversation Compacted\n"
	summaryContent += fmt.Sprintf("Earlier tool turns were executed and compacted to preserve context space.\n</context-summary>")

	summaryEntry := HistoryEntry{
		Kind:   EntryKindMessage,
		Source: "runtime",
		Message: &ModelInputMessage{
			Role:    RoleAssistant,
			Content: summaryContent,
		},
	}

	newEntries := make([]HistoryEntry, 0, 2+len(tail))
	newEntries = append(newEntries, first, summaryEntry)
	newEntries = append(newEntries, tail...)

	r.History.ReplaceMessages(newEntries)
	r.usageTokens = r.History.TokenEstimate()
}

func (r *Runtime) providerMessages() []llm.LLMMessage {
	pm := r.History.ProviderMessages()
	out := make([]llm.LLMMessage, 0, len(pm)+1)
	out = append(out, llm.LLMMessage{Role: llm.RoleSystem, Content: r.System})
	for _, m := range pm {
		lm := llm.LLMMessage{Role: llm.Role(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			lm.ToolCalls = append(lm.ToolCalls, llm.ToolCall{
				ID: tc.ID, Type: "function",
				Function: llm.ToolFunction{Name: tc.Name, Arguments: tc.ArgsJSON},
			})
		}
		out = append(out, lm)
	}
	return out
}

func (r *Runtime) providerTools() []llm.ToolDefinition {
	contracts := r.Registry.Contracts()
	out := make([]llm.ToolDefinition, 0, len(contracts))
	for _, c := range contracts {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)
		schema, _ := c["input_schema"].(map[string]any)
		out = append(out, llm.ToolDefinition{
			Type:     "function",
			Function: llm.FunctionSpec{Name: name, Description: desc, Parameters: schema},
		})
	}
	return out
}

// recordUsage folds provider token stats into rolling metrics and emits step stats.
func (r *Runtime) recordUsage(resp *llm.LLMResponse, step int) {
	if resp == nil {
		return
	}
	u := resp.TokenUsage
	u.Normalize()

	r.lastPromptTokens = int64(u.PromptTokens)
	r.lastCachedTokens = int64(u.CachedTokens)
	if r.lastPromptTokens > 0 {
		r.usageTokens = r.lastPromptTokens + int64(u.CompletionTokens)
	}

	r.totalPromptTokens += int64(u.PromptTokens)
	r.totalCompletionTokens += int64(u.CompletionTokens)
	r.totalCachedTokens += int64(u.CachedTokens)

	model := ""
	if r.LLM != nil {
		model = r.LLM.GetActiveModel()
	}

	var hitRate float64
	if u.PromptTokens > 0 {
		hitRate = float64(u.CachedTokens) / float64(u.PromptTokens)
		if hitRate > 1.0 {
			hitRate = 1.0
		}
	}

	cost := llm.EstimateCost(model, u.PromptTokens, u.CompletionTokens, u.CachedTokens)
	savings, savingsPct := llm.EstimateSavings(model, u.PromptTokens, u.CompletionTokens, u.CachedTokens)

	stats := StepUsageStats{
		Step:                  step,
		Model:                 model,
		PromptTokens:          u.PromptTokens,
		CompletionTokens:      u.CompletionTokens,
		CachedTokens:          u.CachedTokens,
		PromptCacheHitTokens:  u.PromptCacheHitTokens,
		PromptCacheMissTokens: u.PromptCacheMissTokens,
		CacheHitRate:          hitRate,
		TotalTokens:           u.TotalTokens,
		EstimatedCost:         cost,
		SavingsCost:           savings,
		SavingsPct:            savingsPct,
	}

	if h, ok := r.Stream.(StepStatsHandler); ok && h != nil {
		h.OnStepStats(stats)
	}
}

func (r *Runtime) UsageTokens() int64 { return r.usageTokens }
func (r *Runtime) TotalPromptTokens() int64 { return r.totalPromptTokens }
func (r *Runtime) TotalCompletionTokens() int64 { return r.totalCompletionTokens }
func (r *Runtime) TotalCachedTokens() int64 { return r.totalCachedTokens }
func (r *Runtime) CacheHitRate() float64 {
	if r.totalPromptTokens <= 0 {
		return 0.0
	}
	rate := float64(r.totalCachedTokens) / float64(r.totalPromptTokens)
	if rate > 1.0 {
		return 1.0
	}
	return rate
}
