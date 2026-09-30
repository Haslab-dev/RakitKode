package agent

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"

	"github.com/Haslab-dev/RakitKode/internal/agent/prompt"
	agenttools "github.com/Haslab-dev/RakitKode/internal/agent/tools"
	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/history"
	"github.com/Haslab-dev/RakitKode/internal/message"
	"github.com/Haslab-dev/RakitKode/internal/permission"
	"github.com/Haslab-dev/RakitKode/internal/session"
	"github.com/Haslab-dev/RakitKode/internal/skills"
)

//go:embed templates/*.tpl
//go:embed templates/title.md
//go:embed templates/summary.md
var templatesFS embed.FS

// loadTemplate reads an embedded template by name.
func loadTemplate(name string) string {
	data, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		return ""
	}
	return string(data)
}

// auto-summarization thresholds (mirrors crush).
const (
	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.2
	loopDetectionWindowSize     = 12
	loopDetectionMaxRepeats     = 8
)

// Model bundles the fantasy language model with its metadata.
type Model struct {
	Model      fantasy.LanguageModel
	ProviderID string
	Cfg        config.Model
}

// RunComplete is the terminal event of one run.
type RunComplete struct {
	SessionID string
	RunID     string
	MessageID string
	Text      string
	Error     string
	Cancelled bool
}

// AgentCall is one turn request.
type AgentCall struct {
	SessionID string
	RunID     string
	Prompt    string
}

// SessionAgent runs LLM conversations per session.
type SessionAgent struct {
	cfg         *config.Store
	sessions    session.Service
	messages    message.Service
	files       history.Service
	filetracker filetracker.Service
	permissions permission.Service

	largeModel Model
	smallModel Model
	tools      []fantasy.AgentTool
	systemMu   sync.RWMutex

	// loadedSkills tracks which skills were read this session (via the
	// view tool on SKILL.md paths).
	loadedSkills *skills.Tracker

	activeMu   sync.Mutex
	activeRuns map[string]*activeRun

	// RunCompleteFeed is the terminal event broker.
	RunCompleteFeed RunCompleteFeed
}

// RunCompleteFeed publishes terminal run events.
type RunCompleteFeed interface {
	PublishRunComplete(RunComplete)
}

// BrokerFeed adapts a pubsub broker.
type BrokerFeed struct {
	Publish func(RunComplete)
}

// PublishRunComplete implements RunCompleteFeed.
func (b BrokerFeed) PublishRunComplete(rc RunComplete) { b.Publish(rc) }

type activeRun struct {
	cancel context.CancelFunc
}

// NewSessionAgent wires the agent. Call SetTools/SetModel before Run.
func NewSessionAgent(
	cfg *config.Store,
	sessions session.Service,
	messages message.Service,
	files history.Service,
	filetracker filetracker.Service,
	permissions permission.Service,
) *SessionAgent {
	return &SessionAgent{
		cfg:          cfg,
		sessions:     sessions,
		messages:     messages,
		files:        files,
		filetracker:  filetracker,
		permissions:  permissions,
		activeRuns:   make(map[string]*activeRun),
		loadedSkills: skills.NewTracker(nil),
	}
}

// SetTools replaces the tool palette.
func (a *SessionAgent) SetTools(tools []fantasy.AgentTool) {
	a.systemMu.Lock()
	a.tools = tools
	a.systemMu.Unlock()
}

// SetModel replaces the primary model.
func (a *SessionAgent) SetModel(m Model) {
	a.systemMu.Lock()
	a.largeModel = m
	a.systemMu.Unlock()
}

// SetSmallModel replaces the utility model (title generation).
func (a *SessionAgent) SetSmallModel(m Model) {
	a.systemMu.Lock()
	a.smallModel = m
	a.systemMu.Unlock()
}

// BuildSystemPrompt renders the coder prompt for the working dir.
func (a *SessionAgent) BuildSystemPrompt() (string, error) {
	return prompt.Build("coder", loadTemplate("coder.md.tpl"), a.cfg.WorkingDir(), a.cfg.Config())
}

// IsBusy reports whether a run is active for the session.
func (a *SessionAgent) IsBusy(sessionID string) bool {
	a.activeMu.Lock()
	defer a.activeMu.Unlock()
	_, busy := a.activeRuns[sessionID]
	return busy
}

// Cancel aborts the active run of a session.
func (a *SessionAgent) Cancel(sessionID string) {
	a.activeMu.Lock()
	run, ok := a.activeRuns[sessionID]
	a.activeMu.Unlock()
	if ok {
		run.cancel()
	}
}

// Run executes one agent turn end-to-end: persist the user message,
// stream the model, execute tools, persist everything, and publish a
// terminal RunComplete.
func (a *SessionAgent) Run(ctx context.Context, call AgentCall) (*fantasy.AgentResult, error) {
	if call.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	a.activeMu.Lock()
	if _, busy := a.activeRuns[call.SessionID]; busy {
		a.activeMu.Unlock()
		return nil, ErrSessionBusy
	}
	runCtx, cancel := context.WithCancel(ctx)
	ar := &activeRun{cancel: cancel}
	a.activeRuns[call.SessionID] = ar
	a.activeMu.Unlock()

	defer func() {
		a.activeMu.Lock()
		delete(a.activeRuns, call.SessionID)
		a.activeMu.Unlock()
		cancel()
	}()

	a.systemMu.RLock()
	model := a.largeModel
	tools := append([]fantasy.AgentTool(nil), a.tools...)
	a.systemMu.RUnlock()

	sess, err := a.sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Generate a title from the first real prompt.
	msgs, err := a.getSessionMessages(ctx, sess)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}
	if !hasUserTextMessage(msgs) {
		titleCtx := context.WithoutCancel(ctx)
		go a.GenerateTitle(titleCtx, call.SessionID, call.Prompt)
	}

	// Persist the user message.
	if _, err := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: call.Prompt},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to create user message: %w", err)
	}

	systemPrompt, err := a.BuildSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("failed to build system prompt: %w", err)
	}

	// Refresh the message list (now includes the new user message).
	msgs, err = a.getSessionMessages(ctx, sess)
	if err != nil {
		return nil, err
	}
	historyMsgs, _ := a.preparePrompt(msgs)

	runCtx = context.WithValue(runCtx, agenttools.SessionIDContextKey, call.SessionID)

	currentAssistant, err := a.messages.Create(runCtx, call.SessionID, message.CreateMessageParams{
		Role:    message.Assistant,
		Parts:   []message.ContentPart{},
		Model:   model.Cfg.ID,
		Provider: model.ProviderID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create assistant message: %w", err)
	}

	var currentMu sync.Mutex
	current := &currentAssistant
	persist := func(m message.Message) {
		if err := a.messages.Update(runCtx, m); err != nil {
			slog.Error("Failed to persist message", "error", err)
		}
	}

	shouldSummarize := false
	agent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithTools(tools...),
	)

	result, err := agent.Stream(runCtx, fantasy.AgentStreamCall{
		Prompt:   call.Prompt,
		Messages: historyMsgs,
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
			prepared := fantasy.PrepareStepResult{Messages: options.Messages}
			callContext = context.WithValue(callContext, agenttools.MessageIDContextKey, current.ID)
			return callContext, prepared, nil
		},
		OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			current.AppendReasoningContent(reasoning.Text)
			persist(*current)
			return nil
		},
		OnReasoningDelta: func(id string, text string) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			current.AppendReasoningContent(text)
			persist(*current)
			return nil
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			if anthropicData, ok := reasoning.ProviderMetadata[anthropic.Name]; ok {
				if md, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok {
					current.AppendReasoningSignature(md.Signature)
				}
			}
			current.FinishThinking()
			persist(*current)
			return nil
		},
		OnTextDelta: func(id string, text string) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			if len(current.Parts) == 0 {
				text = strings.TrimPrefix(text, "\n")
			}
			current.AppendContent(text)
			persist(*current)
			return nil
		},
		OnToolInputStart: func(id string, toolName string) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			current.AddToolCall(message.ToolCall{ID: id, Name: toolName, Finished: false})
			persist(*current)
			return nil
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			currentMu.Lock()
			defer currentMu.Unlock()
			current.AddToolCall(message.ToolCall{
				ID:       tc.ToolCallID,
				Name:     tc.ToolName,
				Input:    tc.Input,
				Finished: true,
			})
			persist(*current)
			return nil
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			toolResult := message.ToolResult{
				ToolCallID: result.ToolCallID,
				Name:       result.ToolName,
				Content:    toolResultText(result),
				IsError:    result.Result.GetType() == fantasy.ToolResultContentTypeError,
			}
			_, err := a.messages.Create(context.WithoutCancel(runCtx), current.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					toolResult,
				},
			})
			return err
		},
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			finishReason := message.FinishReasonUnknown
			switch stepResult.FinishReason {
			case fantasy.FinishReasonLength:
				finishReason = message.FinishReasonMaxTokens
			case fantasy.FinishReasonStop:
				finishReason = message.FinishReasonEndTurn
			case fantasy.FinishReasonToolCalls:
				finishReason = message.FinishReasonToolUse
			case fantasy.FinishReasonContentFilter:
				finishReason = message.FinishReasonContentFilter
			}
			currentMu.Lock()
			defer currentMu.Unlock()
			current.AddFinish(finishReason, "", "")
			persist(*current)

			// Track usage on the session.
			updated, getErr := a.sessions.Get(context.WithoutCancel(runCtx), call.SessionID)
			if getErr != nil {
				return getErr
			}
			updated.PromptTokens += stepResult.Usage.InputTokens
			updated.CompletionTokens += stepResult.Usage.OutputTokens
			if _, saveErr := a.sessions.Save(context.WithoutCancel(runCtx), updated); saveErr != nil {
				slog.Error("Failed to save session usage", "error", saveErr)
			}
			return nil
		},
		StopWhen: []fantasy.StopCondition{
			func(_ []fantasy.StepResult) bool {
				cw := model.Cfg.ContextWindow
				if cw == 0 {
					return false
				}
				latest, getErr := a.sessions.Get(context.WithoutCancel(runCtx), call.SessionID)
				if getErr != nil {
					return false
				}
				tokens := latest.CompletionTokens + latest.PromptTokens
				remaining := cw - tokens
				var threshold int64
				if cw > largeContextWindowThreshold {
					threshold = largeContextWindowBuffer
				} else {
					threshold = int64(float64(cw) * smallContextWindowRatio)
				}
				if remaining <= threshold {
					shouldSummarize = true
					return true
				}
				return false
			},
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
		},
	})

	// Terminal bookkeeping shared by success and error paths.
	finalize := func(err error) (*fantasy.AgentResult, error) {
		if err == nil {
			a.publishComplete(RunComplete{
				SessionID: call.SessionID,
				RunID:     call.RunID,
				MessageID: current.ID,
				Text:      current.Content().Text,
			})
			return result, nil
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
		defer cleanupCancel()

		currentMu.Lock()
		current.FinishThinking()

		// Close any unfinished tool calls with error results so the
		// transcript stays valid for the next turn.
		for _, tc := range current.ToolCalls() {
			if !tc.Finished {
				tc.Finished = true
				current.AddToolCall(tc)
				_ = a.messages.Update(cleanupCtx, *current)
			}
			content := "There was an error while executing the tool"
			if errors.Is(err, context.Canceled) {
				content = "Error: user cancelled assistant tool calling"
			}
			if _, createErr := a.messages.Create(cleanupCtx, current.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					message.ToolResult{ToolCallID: tc.ID, Name: tc.Name, Content: content, IsError: true},
				},
			}); createErr != nil {
				slog.Error("Failed to persist tool error result", "error", createErr)
			}
		}

		if errors.Is(err, context.Canceled) {
			current.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
		} else {
			current.AddFinish(message.FinishReasonError, "Provider Error", err.Error())
		}
		_ = a.messages.Update(cleanupCtx, *current)
		msgID := current.ID
		text := current.Content().Text
		currentMu.Unlock()

		a.publishComplete(RunComplete{
			SessionID: call.SessionID,
			RunID:     call.RunID,
			MessageID: msgID,
			Text:      text,
			Error:     err.Error(),
			Cancelled: errors.Is(err, context.Canceled),
		})
		return result, err
	}

	if err != nil {
		return finalize(err)
	}

	if shouldSummarize {
		if summarizeErr := a.Summarize(context.WithoutCancel(runCtx), call.SessionID); summarizeErr != nil {
			slog.Error("Auto-summarize failed", "error", summarizeErr)
		}
	}
	return finalize(nil)
}

// publishComplete emits a terminal run event.
func (a *SessionAgent) publishComplete(rc RunComplete) {
	if a.RunCompleteFeed != nil {
		a.RunCompleteFeed.PublishRunComplete(rc)
	}
}

// Summarize condenses the conversation into a summary message and
// points the session at it.
func (a *SessionAgent) Summarize(ctx context.Context, sessionID string) error {
	a.systemMu.RLock()
	model := a.largeModel
	a.systemMu.RUnlock()

	sess, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, sess)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	historyMsgs, _ := a.preparePrompt(msgs)

	agent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(loadTemplate("summary.md")),
	)
	summaryMessage, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:             message.Assistant,
		Model:            model.Cfg.ID,
		Provider:         model.ProviderID,
		IsSummaryMessage: true,
	})
	if err != nil {
		return err
	}

	resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt:   "Provide a detailed summary of our conversation above.",
		Messages: historyMsgs,
		OnTextDelta: func(id string, text string) error {
			summaryMessage.AppendContent(text)
			return a.messages.Update(ctx, summaryMessage)
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return a.messages.Delete(ctx, summaryMessage.ID)
		}
		summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
		_ = a.messages.Update(ctx, summaryMessage)
		return err
	}

	summaryMessage.AddFinish(message.FinishReasonEndTurn, "", "")
	if err := a.messages.Update(ctx, summaryMessage); err != nil {
		return err
	}

	sess.SummaryMessageID = summaryMessage.ID
	sess.CompletionTokens += resp.TotalUsage.OutputTokens
	sess.PromptTokens = 0
	if _, err := a.sessions.Save(ctx, sess); err != nil {
		return err
	}
	return nil
}

// GenerateTitle derives a session title from the first prompt using the
// small model, falling back to a truncation when unavailable.
func (a *SessionAgent) GenerateTitle(ctx context.Context, sessionID, userPrompt string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Title generation panicked", "panic", r)
		}
	}()
	if userPrompt == "" {
		return
	}

	title := truncateTitle(userPrompt)
	a.systemMu.RLock()
	model := a.smallModel
	a.systemMu.RUnlock()

	if model.Model != nil {
		agent := fantasy.NewAgent(
			model.Model,
			fantasy.WithSystemPrompt(loadTemplate("title.md")),
		)
		result, err := agent.Stream(ctx, fantasy.AgentStreamCall{
			Prompt: userPrompt,
			StopWhen: []fantasy.StopCondition{
				fantasy.StepCountIs(1),
			},
		})
		if err == nil && len(result.Steps) > 0 {
			generated := result.Steps[len(result.Steps)-1].Content.Text()
			generated = strings.TrimSpace(generated)
			if generated != "" {
				title = cleanTitle(generated)
			}
		} else if err != nil {
			slog.Debug("Title generation failed, using truncation", "error", err)
		}
	}
	if err := a.sessions.Rename(ctx, sessionID, title); err != nil {
		slog.Error("Failed to save session title", "error", err)
	}
}

var thinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)
var orphanThinkTagRegex = regexp.MustCompile(`</?think>`)
var titleSanitizer = regexp.MustCompile(`[\n\r]`)

func cleanTitle(title string) string {
	title = thinkTagRegex.ReplaceAllString(title, "")
	title = orphanThinkTagRegex.ReplaceAllString(title, "")
	title = titleSanitizer.ReplaceAllString(title, " ")
	title = strings.TrimSpace(title)
	return truncateTitle(title)
}

func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\n.!?"); idx > 0 {
		s = s[:idx]
	}
	runes := []rune(s)
	if len(runes) > 50 {
		s = string(runes[:50])
	}
	return s
}

// getSessionMessages reads the messages a compacted session still sends.
func (a *SessionAgent) getSessionMessages(ctx context.Context, sess session.Session) ([]message.Message, error) {
	msgs, err := a.messages.ListFromSummary(ctx, sess.ID, sess.SummaryMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	if sess.SummaryMessageID != "" {
		for i, m := range msgs {
			if m.ID == sess.SummaryMessageID {
				msgs = msgs[i:]
				msgs[0].Role = message.User
				break
			}
		}
	}
	return msgs, nil
}

// preparePrompt converts stored messages into fantasy history, keeping
// tool results adjacent to their assistant messages.
func (a *SessionAgent) preparePrompt(msgs []message.Message) ([]fantasy.Message, error) {
	var history []fantasy.Message
	history = append(history, fantasy.NewUserMessage(fmt.Sprintf(
		"<system_reminder>%s</system_reminder>",
		`This is a reminder that your todo list is empty. DO NOT mention this to the user explicitly because they are already aware.
If you are working on tasks that would benefit from a todo list please use the "todos" tool to create one.
If not, please feel free to ignore. Again do not mention this message to the user.`,
	)))

	knownToolCallIDs := make(map[string]struct{})
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			knownToolCallIDs[tc.ID] = struct{}{}
		}
	}
	toolResultsByCall := make(map[string][]fantasy.MessagePart)
	for _, m := range msgs {
		if m.Role != message.Tool {
			continue
		}
		for _, aiMsg := range m.ToAIMessage() {
			for _, part := range aiMsg.Content {
				tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
				if !ok {
					continue
				}
				if _, known := knownToolCallIDs[tr.ToolCallID]; !known {
					continue
				}
				toolResultsByCall[tr.ToolCallID] = append(toolResultsByCall[tr.ToolCallID], part)
			}
		}
	}

	for _, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		if m.Role == message.Assistant && len(m.ToolCalls()) == 0 && m.Content().Text == "" && m.ReasoningContent().Thinking == "" {
			continue
		}
		if m.Role == message.Tool {
			continue
		}
		history = append(history, m.ToAIMessage()...)

		if m.Role == message.Assistant && len(m.ToolCalls()) > 0 {
			for _, tc := range m.ToolCalls() {
				parts, ok := toolResultsByCall[tc.ID]
				if !ok || len(parts) == 0 {
					continue
				}
				history = append(history, fantasy.Message{
					Role:    fantasy.MessageRoleTool,
					Content: parts,
				})
			}
		}
	}
	return history, nil
}

// hasUserTextMessage reports whether any user message carries text.
func hasUserTextMessage(msgs []message.Message) bool {
	for _, msg := range msgs {
		if msg.Role != message.User {
			continue
		}
		for _, part := range msg.Parts {
			if tc, ok := part.(message.TextContent); ok && tc.Text != "" {
				return true
			}
		}
	}
	return false
}

// hasRepeatedToolCalls detects the model repeating the same failed tool
// call pattern and halts the loop.
func hasRepeatedToolCalls(steps []fantasy.StepResult, windowSize, maxRepeats int) bool {
	type callKey struct{ name, input string }
	var window []callKey
	for _, step := range steps {
		for _, tc := range step.Content.ToolCalls() {
			window = append(window, callKey{tc.ToolName, tc.Input})
		}
	}
	if len(window) < windowSize {
		return false
	}
	window = window[len(window)-windowSize:]
	counts := make(map[callKey]int)
	for _, k := range window {
		counts[k]++
	}
	for _, n := range counts {
		if n >= maxRepeats {
			return true
		}
	}
	return false
}

// toolResultText flattens a tool result for storage.
func toolResultText(result fantasy.ToolResultContent) string {
	switch out := result.Result.(type) {
	case fantasy.ToolResultOutputContentText:
		return out.Text
	case fantasy.ToolResultOutputContentMedia:
		return fmt.Sprintf("[media: %s]", out.MediaType)
	case fantasy.ToolResultOutputContentError:
		if out.Error != nil {
			return out.Error.Error()
		}
		return "tool error"
	default:
		return ""
	}
}

// ErrSessionBusy is returned when a session already has an active run.
var ErrSessionBusy = errors.New("session is busy")
