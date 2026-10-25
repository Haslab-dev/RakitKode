// Package tui implements the interactive terminal UI for the rakit engine:
// a streaming transcript viewport keyed by turn, a multi-line input, a slash
// command menu, an interactive model picker, an inline tool-approval prompt,
// and structured ask rendering.
//
// Engine events arrive on the event bus from the turn goroutine; the pump
// marshals them into bubbletea messages through a channel so Update never
// runs on the publisher's goroutine.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/tools"
)

// agentUpdatedEvent is published by the manager as a raw string (there is no
// exported const in the events package yet).
var agentUpdatedEvent = events.EventType("agent:updated")

// statusWorking is the spinner label shown while a turn runs.
const statusWorking = "◍ working…"

// workingVerbs rotate behind the spinner while a turn runs (one step every
// ~3 spinner seconds), Claude-Code style.
var workingVerbs = []string{
	"assembling", "forging", "compiling", "wiring", "exploring",
	"planning", "refactoring", "polishing", "shipping", "debugging",
	"herding tests", "reading the room",
}

// Model is the Bubble Tea model of the chat UI.
type Model struct {
	eng       *engine.Engine
	mgr       *agent.Manager
	sessionID string

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	ready  bool
	width  int
	height int

	busy            bool
	cancelRequested bool
	status          string
	modelName       string

	// transcript is the rendered history; streamed blocks mutate in place.
	transcript []block
	// cur holds the mutable block indexes of the assistant message that is
	// streaming right now (the delta events carry no message id, so the
	// current stream is tracked instead of a per-id map).
	cur        streamBlocks
	streamOpen bool
	// thinkStart clocks the current reasoning block for "thought for Ns".
	thinkStart time.Time
	// toolLines maps tool call ids to transcript indexes.
	toolLines map[string]int
	// blockCursor selects an expandable block for ctrl+up/down navigation.
	blockCursor       int
	blockCursorActive bool

	// input history (persisted under <home>/history)
	history   []string
	histIdx   int
	histDraft string

	// @-mention completion state
	atOpen     bool
	atItems    []string
	atSelected int
	atStart    int
	fileIndex  []string

	// pendingApproval mirrors the session's PendingTools while its state is
	// awaiting_approval; y/a/n answer via Manager.RespondApproval.
	pendingApproval []agent.ContentBlock
	// pendingAsk tracks progress through the session's pending questions;
	// number keys pick options, answered via Manager.RespondAsk.
	pendingAsk *askState

	// slash menu state
	menuOpen     bool
	menuItems    []slashItem
	menuSelected int
	// pendingSlashArgs holds the argument words of the slash command being
	// dispatched (set by handleSlashInput, read by command actions).
	pendingSlashArgs []string

	// /model picker state (interactive; esc returns to the input)
	pickerOpen bool
	picker     modelPicker

	// /resume session picker
	sessPicker sessionPicker

	// usage counters pulled from the session snapshot
	tokensIn      int
	tokensOut     int
	contextTokens int
	cacheHit      float64
	estimatedCost float64

	// header cache: mcp servers up/total + connected tool count, skills count
	mcpUp     int
	mcpTotal  int
	mcpTools  int
	skillCnt  int
	effortSet string // last /effort value applied ("" = engine default)

	// tick counts spinner frames; it drives the rotating working verb.
	tick int

	// turnStart clocks the running turn for the elapsed activity line.
	turnStart time.Time

	// stuckBottom tracks whether the transcript should follow new output:
	// true until the user scrolls up, restored by scrolling back down.
	stuckBottom bool

	quitRequested bool
}

// streamBlocks holds the transcript indexes of one assistant message's
// mutable rendering blocks (-1 when the part has not appeared yet).
type streamBlocks struct {
	reasoning int
	text      int
}

// New builds the TUI model. A nil engine produces a display-only model that
// renders and answers keys without touching the manager (used by tests).
func New(e *engine.Engine, sessionID string) Model {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Ask anything…  (/ commands · shift+enter newline)"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 8
	styles := textarea.DefaultStyles(true)
	styles.Focused.Prompt = userStyle
	styles.Blurred.Prompt = userStyle
	ta.SetStyles(styles)
	ta.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.Spinner{
			Frames: spinner.MiniDot.Frames,
			FPS:    time.Second / 5,
		}),
		spinner.WithStyle(spinnerStyle),
	)

	m := Model{
		eng:         e,
		sessionID:   sessionID,
		textarea:    ta,
		spinner:     sp,
		viewport:    viewport.New(),
		status:      "ready",
		cur:         streamBlocks{reasoning: -1, text: -1},
		toolLines:   map[string]int{},
		blockCursor: -1,
		stuckBottom: true,
	}
	// Soft-wrap long transcript lines; the default (horizontal scroll)
	// shifts every row sideways once one line overflows.
	m.viewport.SoftWrap = true
	if e != nil {
		m.mgr = e.Manager()
		m.loadHistory()
	}
	m.pullSession()
	m.appendSessionHeader()
	m.hydrateTranscript()
	return m
}

// Run executes the TUI program for one session: it subscribes to the engine
// event bus, runs the bubbletea loop, and blocks until the user quits.
func Run(e *engine.Engine, sessionID string) error {
	m := New(e, sessionID)
	p := tea.NewProgram(m)
	pump := startPump(e.Bus(), p)
	defer pump.stop()
	_, err := p.Run()
	return err
}

// noticeMsg carries the output of an async slash action (e.g. /mcp
// reconnect, /compact) back into the transcript once the goroutine finishes.
type noticeMsg struct{ lines []string }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.spinner.Tick) }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.invalidateRendered()
		m.syncLayout()

	case noticeMsg:
		for _, line := range msg.lines {
			m.appendBlock(line)
		}
		m.pullSession()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.tick++
		if m.busy {
			// Keep running tool blocks and the rotating verb animated.
			m.refreshViewport()
		}
		// Exactly one tick chain: spinner.Update already returns the next
		// tick command. Scheduling an extra spinner.Tick here multiplied the
		// frame rate on every frame.
		return m, cmd

	case tea.MouseWheelMsg:
		// The viewport gets mouse input directly; keys never flow into it
		// (its default keymap binds plain letters like f/b/d/j/k to
		// scrolling, which would scroll the transcript while typing).
		m.handleWheel(msg)
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		key, ok := sanitizeKeyPress(msg)
		if !ok {
			return m, nil
		}
		msg = key
		if handled, cmd := m.interceptKey(msg); handled {
			return m, cmd
		}

	case tea.CursorPositionMsg:
		// Terminal cursor-position report; never feed it to the textarea.
		return m, nil

	case busEvent:
		m.handleBusEvent(msg.ev)
		return m, nil
	}

	// Feed the textarea; live-trigger the slash menu on "/" input.
	heightBefore := m.textarea.Height()
	overlayBefore := m.overlayHeight()
	valueBefore := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds := []tea.Cmd{cmd}
	valueAfter := m.textarea.Value()
	if cleaned := stripCPR(valueAfter); cleaned != valueAfter {
		m.textarea.SetValue(cleaned)
		valueAfter = cleaned
	}
	inModelCommand := valueAfter == "/model" || strings.HasPrefix(valueAfter, "/model ")
	switch {
	case inModelCommand:
		// /model drives the interactive picker, not the command menu.
		// Typed characters after "/model " filter the list instead of
		// closing it.
		m.menuOpen = false
		query := strings.TrimSpace(strings.TrimPrefix(valueAfter, "/model"))
		if !m.pickerOpen {
			m.openModelPicker(query)
			m.pickerOpen = true
		} else {
			m.filterModelPicker(query)
		}
	case m.pickerOpen:
		// Left the /model command (edited past it) — close the picker.
		m.pickerOpen = false
		m.picker.open = false
	case !m.menuOpen && strings.HasPrefix(valueAfter, "/") && len(valueAfter) > len(valueBefore):
		m.openSlashMenu(strings.TrimPrefix(valueAfter, "/"))
	case m.menuOpen:
		if strings.HasPrefix(valueAfter, "/") {
			m.openSlashMenu(strings.TrimPrefix(valueAfter, "/"))
		} else if valueAfter == "" {
			m.menuOpen = false
		}
	}
	if m.textarea.Height() != heightBefore || m.overlayHeight() != overlayBefore {
		// The input grew, or a menu/picker opened, closed, or filtered:
		// the footer's height changed, so re-split the vertical space.
		m.syncLayout()
	}
	if !m.menuOpen && !m.pickerOpen {
		m.updateAtMenu()
	}
	return m, tea.Batch(cmds...)
}

// handleWheel tracks wheel direction for stick-to-bottom before the viewport
// consumes the event.
func (m *Model) handleWheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.stuckBottom = false
	case tea.MouseWheelDown:
		m.stuckBottom = true
	}
}

// interceptKey consumes keys with dedicated meanings (prompts, menu, submit,
// quit). It reports whether the key was handled; unhandled keys flow through
// to the input components.
func (m *Model) interceptKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()

	// Inline approval prompt: swallow everything except the answers.
	if len(m.pendingApproval) > 0 {
		switch key {
		case "y":
			m.respondApproval(true, false)
			m.appendBlock(toolDotOKStyle.Render("  ✓ granted"))
		case "a":
			m.respondApproval(true, true)
			m.appendBlock(toolDotOKStyle.Render("  ✓ granted (auto-approve on)"))
		case "n":
			m.respondApproval(false, false)
			m.appendBlock(toolDotErrStyle.Render("  ✗ denied"))
		case "esc", "ctrl+c":
			m.respondApproval(false, false)
			m.appendBlock(toolDotErrStyle.Render("  ✗ denied (esc)"))
		case "ctrl+d":
			return true, tea.Quit
		}
		return true, nil
	}

	// Structured ask: number keys pick options for the current question.
	if m.pendingAsk != nil {
		switch key {
		case "ctrl+c", "ctrl+d":
			return true, tea.Quit
		}
		return true, m.interceptAskKey(key)
	}

	// Model picker: arrows/enter/esc handled here; letters fall through to
	// the textarea so the list filters as you type.
	if m.pickerOpen {
		switch key {
		case "up", "ctrl+p":
			m.movePickerSelection(-1)
			return true, nil
		case "down", "ctrl+n":
			m.movePickerSelection(1)
			return true, nil
		case "esc":
			m.pickerOpen = false
			m.picker.open = false
			m.textarea.Reset()
			return true, nil
		case "enter":
			model := m.pickerSelection()
			m.pickerOpen = false
			m.picker.open = false
			m.textarea.Reset()
			if model != "" {
				m.appendBlock(userStyle.Render("❯ /model " + m.displayModelName(model)))
				for _, line := range m.applyModelSwitch(model) {
					m.appendBlock(line)
				}
			}
			return true, nil
		}
		return false, nil
	}

	if m.menuOpen {
		switch key {
		case "up", "ctrl+p":
			m.moveMenuSelection(-1)
			return true, nil
		case "down", "ctrl+n":
			m.moveMenuSelection(1)
			return true, nil
		case "esc":
			m.menuOpen = false
			m.textarea.Reset()
			return true, nil
		case "enter", "tab":
			return true, m.menuEnter(key)
		}
	}

	if m.atOpen {
		switch key {
		case "up", "ctrl+p":
			if m.atSelected > 0 {
				m.atSelected--
			}
			return true, nil
		case "down", "ctrl+n":
			if m.atSelected < len(m.atItems)-1 {
				m.atSelected++
			}
			return true, nil
		case "esc":
			m.atOpen = false
			return true, nil
		case "tab", "enter":
			m.completeAtMention()
			return true, nil
		}
	}

	if m.sessPicker.open {
		if key == "ctrl+c" || key == "ctrl+d" {
			return true, tea.Quit
		}
		m.handleSessionPickerKey(key)
		return true, nil
	}

	// "?" opens the shortcut/command help when the input is empty.
	if key == "?" && m.textarea.Value() == "" {
		lines, _ := actionHelp(m)
		for _, line := range lines {
			m.appendBlock(line)
		}
		return true, nil
	}

	switch key {
	case "shift+tab", "backtab":
		m.cyclePermissionMode()
		return true, nil
	case "pgup":
		m.viewport.PageUp()
		m.stuckBottom = false
		return true, nil
	case "pgdown":
		m.viewport.PageDown()
		m.stuckBottom = true
		return true, nil
	case "home":
		m.viewport.GotoTop()
		m.stuckBottom = false
		return true, nil
	case "end":
		m.viewport.GotoBottom()
		m.stuckBottom = true
		return true, nil
	case "tab":
		// Toggle the selected reasoning block, else the newest one.
		m.toggleReasoning()
		return true, nil
	case "ctrl+o":
		// Toggle the selected tool block, else the newest finished one.
		m.toggleToolOutput()
		return true, nil
	case "ctrl+up":
		m.moveBlockCursor(-1)
		return true, nil
	case "ctrl+down":
		m.moveBlockCursor(1)
		return true, nil
	case "ctrl+k":
		m.openSlashMenu("")
		return true, nil
	case "ctrl+p":
		m.historyPrev()
		return true, nil
	case "ctrl+n":
		m.historyNext()
		return true, nil
	case "ctrl+c":
		if m.busy && !m.cancelRequested {
			// First press cancels the turn; the second quits.
			m.cancelRequested = true
			m.status = "cancelling… (ctrl+c again quits)"
			if m.mgr != nil {
				m.mgr.StopTurn(m.sessionID)
			}
			return true, nil
		}
		return true, tea.Quit
	case "ctrl+d":
		return true, tea.Quit
	case "esc":
		switch {
		case m.busy && !m.cancelRequested:
			// Esc interrupts the running turn first.
			m.cancelRequested = true
			m.status = "cancelling… (esc again quits)"
			if m.mgr != nil {
				m.mgr.StopTurn(m.sessionID)
			}
		case m.menuOpen:
			m.menuOpen = false
			m.textarea.Reset()
		case m.textarea.Value() != "":
			m.textarea.Reset()
		default:
			return true, tea.Quit
		}
		return true, nil
	case "enter":
		return true, m.submit()
	case "shift+enter", "alt+enter", "ctrl+j":
		// Newline inside the draft; enter still submits. shift+enter only
		// reaches the app on terminals with keyboard enhancement support.
		m.textarea.InsertString("\n")
		return true, nil
	}
	return false, nil
}

// cyclePermissionMode rotates the session mode: build → edit → yolo → plan.
func (m *Model) cyclePermissionMode() {
	if m.eng == nil {
		return
	}
	order := []string{"build", "edit", "yolo", "plan"}
	cur := m.eng.PermissionModeFor(m.sessionID)
	next := "build"
	for i, mode := range order {
		if mode == cur {
			next = order[(i+1)%len(order)]
			break
		}
	}
	if err := m.eng.SetPermissionMode(m.sessionID, next); err != nil {
		m.appendBlock(errorStyle.Render("mode: " + err.Error()))
		return
	}
	m.appendBlock(statusStyle.Render("● permission: " + next))
}

// cprPattern matches a terminal cursor-position report (DSR response):
// ESC[<row>;<col>R, with the ESC sometimes stripped by the input parser.
var cprPattern = regexp.MustCompile(`\x1b?\[\d+;\d+R`)

func stripCPR(s string) string {
	if !strings.Contains(s, "R") || !strings.Contains(s, ";") {
		return s
	}
	return cprPattern.ReplaceAllString(s, "")
}

// sanitizeKeyPress drops (or cleans) key messages that are really CPR
// responses. Apple Terminal occasionally delivers them as printable text when
// the escape sequence is split across reads.
func sanitizeKeyPress(msg tea.KeyPressMsg) (tea.KeyPressMsg, bool) {
	text := msg.Text
	if text == "" || (!strings.Contains(text, "R") && !strings.Contains(text, ";")) {
		return msg, true
	}
	cleaned := stripCPR(text)
	if cleaned == text {
		return msg, true
	}
	if cleaned == "" {
		return msg, false
	}
	msg.Text = cleaned
	msg.Code = []rune(cleaned)[0]
	return msg, true
}

// submit sends the current input: slash commands run inline, anything else
// starts one agent turn via the manager.
func (m *Model) submit() tea.Cmd {
	value := strings.TrimSpace(m.textarea.Value())
	if value == "" {
		return nil
	}
	if m.busy {
		// Keep the turn alive and steer it instead of discarding the draft.
		if m.mgr != nil {
			if err := m.mgr.SteerMessage(m.sessionID, value); err == nil {
				m.appendBlock(userStyle.Render("↳ steer ") + value)
				m.textarea.Reset()
				m.textarea.Focus()
				m.status = "steering current turn"
				return nil
			}
		}
		m.status = "turn running — ctrl+c interrupts"
		return nil
	}
	m.textarea.Reset()
	m.textarea.Focus()
	m.menuOpen = false
	m.recordHistory(value)
	m.appendBlock(userStyle.Render("❯ ") + value)
	if strings.HasPrefix(value, "/") {
		return m.runSlashInput(value)
	}
	m.busy = true
	m.cancelRequested = false
	m.turnStart = time.Now()
	m.status = statusWorking
	if m.mgr == nil {
		m.busy = false
		m.status = "ready"
		m.appendBlock(errorStyle.Render("error: no engine attached"))
		return nil
	}
	if err := m.mgr.SendMessage(context.Background(), m.sessionID, value, m.mentionsInDraft(value)); err != nil {
		m.busy = false
		m.status = "ready"
		m.appendBlock(errorStyle.Render("error: " + err.Error()))
	}
	return nil
}

// runSlashInput interprets a "/..." submission already echoed to the transcript.
func (m *Model) runSlashInput(value string) tea.Cmd {
	lines, cmd := m.handleSlashInput(value)
	for _, line := range lines {
		m.appendBlock(line)
	}
	if m.quitRequested {
		return tea.Quit
	}
	return cmd
}

// respondApproval answers the pending tool batch. RespondApproval itself
// enables auto-approve when autoAll is set.
func (m *Model) respondApproval(approve, autoAll bool) {
	if m.mgr == nil {
		m.pendingApproval = nil
		return
	}
	if err := m.mgr.RespondApproval(context.Background(), m.sessionID, approve, autoAll); err != nil {
		m.appendBlock(errorStyle.Render("error: " + err.Error()))
	}
	m.pendingApproval = nil
}

// interceptAskKey applies a key to the current pending question. Every key is
// consumed while the ask prompt is open.
func (m *Model) interceptAskKey(key string) tea.Cmd {
	a := m.pendingAsk
	if a == nil || a.qi >= len(a.questions) {
		m.pendingAsk = nil
		return nil
	}
	q := a.questions[a.qi]
	if n := digit(key); n >= 1 && n <= len(q.Options) {
		if q.Multi {
			a.picks[q.ID] = toggleOption(a.picks[q.ID], q.Options[n-1])
		} else {
			a.picks[q.ID] = []string{q.Options[n-1]}
			m.advanceAsk()
		}
		m.refreshViewport()
		return nil
	}
	if key == "enter" && len(a.picks[q.ID]) > 0 {
		m.advanceAsk()
		return nil
	}
	return nil
}

// advanceAsk moves to the next pending question, or resumes the turn with the
// collected answers when the last one is answered.
func (m *Model) advanceAsk() {
	a := m.pendingAsk
	if a == nil {
		return
	}
	if a.qi < len(a.questions)-1 {
		a.qi++
		return
	}
	answers := make(map[string]any, len(a.picks))
	for id, opts := range a.picks {
		if len(opts) == 1 {
			answers[id] = opts[0]
		} else {
			answers[id] = opts
		}
	}
	m.pendingAsk = nil
	m.appendBlock(statusStyle.Render("  → answered"))
	if m.mgr != nil {
		_ = m.mgr.RespondAsk(m.sessionID, answers)
	}
}

// digit returns 1-9 for the corresponding number key, else 0.
func digit(key string) int {
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		return int(key[0] - '0')
	}
	return 0
}

// toggleOption adds or removes an option from a multi-select answer.
func toggleOption(picks []string, opt string) []string {
	for i, p := range picks {
		if p == opt {
			return append(picks[:i:i], picks[i+1:]...)
		}
	}
	return append(picks, opt)
}

// busEvent wraps one engine bus event for the update loop.
type busEvent struct{ ev events.Event }

// handleBusEvent folds an engine event into the transcript and state.
func (m *Model) handleBusEvent(ev events.Event) {
	if id, _ := ev.Data["session_id"].(string); id != "" && id != m.sessionID {
		return
	}
	switch ev.Type {
	case events.AgentMessageDelta:
		if kind, _ := ev.Data["kind"].(string); kind == "text" {
			delta, _ := ev.Data["delta"].(string)
			m.streamText(delta)
		}
	case events.AgentThinkingDelta:
		delta, _ := ev.Data["delta"].(string)
		m.streamThinking(delta)
	case events.AgentToolStart:
		id, _ := ev.Data["tool_call_id"].(string)
		name, _ := ev.Data["name"].(string)
		args, _ := ev.Data["args"].(map[string]any)
		m.closeStream()
		m.setToolRunning(id, name, args)
	case events.AgentToolEnd:
		id, _ := ev.Data["tool_call_id"].(string)
		name, _ := ev.Data["name"].(string)
		result, _ := ev.Data["result"].(string)
		isErr, _ := ev.Data["is_error"].(bool)
		m.setToolResult(id, name, result, isErr)
		if disp, ok := ev.Data["display"].(map[string]any); ok {
			if diff, ok := disp["diff"].(string); ok && diff != "" {
				m.setToolDiff(id, diff)
			}
		}
	case events.AgentTurnStart:
		m.closeStream()
		m.busy = true
		m.turnStart = time.Now()
		m.status = statusWorking
	case events.AgentTurnEnd:
		m.closeStream()
		m.busy = false
		m.turnStart = time.Time{}
		if len(m.transcript) > 0 && m.transcript[len(m.transcript)-1].content != "" {
			m.appendBlock("")
		}
		m.cancelRequested = false
		if errMsg, _ := ev.Data["error"].(string); errMsg != "" {
			if strings.Contains(strings.ToLower(errMsg), "cancel") {
				m.appendBlock(statusStyle.Render("  ⏹ stopped by user"))
			} else {
				m.appendBlock(errorStyle.Render("error: " + errMsg))
			}
		}
		m.status = "ready"
		m.pullSession()
		m.textarea.Focus()
	case events.AgentAsk, agentUpdatedEvent:
		m.pullSession()
	}
	m.refreshViewport()
}

// pullSession refreshes the cached model name, usage counters, and pending
// approval/ask state from the manager's session snapshot. Safe to call
// often: it is one RLock-guarded map lookup.
func (m *Model) pullSession() {
	if m.mgr == nil {
		return
	}
	sess, ok := m.mgr.GetSession(m.sessionID)
	if !ok {
		return
	}
	m.modelName = sess.Model
	m.tokensIn = sess.TokenUsage.PromptTokens
	m.tokensOut = sess.TokenUsage.CompletionTokens
	m.contextTokens = sess.ContextTokens
	m.cacheHit = sess.CacheHitRate
	m.estimatedCost = sess.EstimatedCost
	if m.estimatedCost <= 0 && (m.tokensIn > 0 || m.tokensOut > 0) {
		m.estimatedCost = llm.EstimateCost(m.modelName, m.tokensIn, m.tokensOut)
	}
	m.refreshEnvCache()
	switch sess.State {
	case agent.StateAwaiting:
		m.pendingApproval = append([]agent.ContentBlock(nil), sess.PendingTools...)
		m.status = "approval needed"
	case agent.StateAwaitingInput:
		if m.pendingAsk == nil || m.pendingAsk.fingerprint() != askFingerprint(sess.PendingQuestions) {
			m.pendingAsk = newAskState(sess.PendingQuestions)
		}
		m.status = "question"
	default:
		m.pendingApproval = nil
		m.pendingAsk = nil
		m.status = statusForBusy(m.busy)
	}
	m.syncLayout()
}

// refreshEnvCache recomputes the header's MCP/skills counters from the
// engine. Cheap enough to run on every session pull.
func (m *Model) refreshEnvCache() {
	m.mcpUp, m.mcpTotal, m.mcpTools, m.skillCnt = 0, 0, 0, 0
	if m.eng == nil {
		return
	}
	for _, s := range m.eng.MCPStatus() {
		m.mcpTotal++
		if s.Enabled && s.Status == "connected" {
			m.mcpUp++
		}
	}
	m.mcpTools = len(m.eng.MCP().ListConnectedTools())
	m.skillCnt = len(m.eng.Skills())
}

// statusForBusy returns the spinner label.
func statusForBusy(busy bool) string {
	if busy {
		return statusWorking
	}
	return "ready"
}

// verb returns the rotating working word shown behind the spinner.
func (m Model) verb() string {
	if len(workingVerbs) == 0 {
		return "working"
	}
	return workingVerbs[(m.tick/30)%len(workingVerbs)]
}

// syncLayout sizes the input and the transcript viewport from the current
// window size, leaving room for the header, the bordered input, the status
// bar, and any visible approval/ask boxes.
func (m *Model) syncLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	taW := m.width - 6
	if taW < 20 {
		taW = 20
	}
	m.textarea.SetWidth(taW)
	vw := m.width - 2
	if vw < 24 {
		vw = 24
	}
	m.viewport.SetWidth(vw)

	h := m.height - m.taLines() - 9 // header + activity + input border + status + breathing room
	if boxes := m.overlayHeight(); boxes > 0 {
		h -= boxes
	}
	if h < 3 {
		h = 3
	}
	m.viewport.SetHeight(h)
	m.refreshViewport()
}

// taLines returns the input's current rendered line count (min 1).
func (m *Model) taLines() int {
	if l := m.textarea.Height(); l > 0 {
		return l
	}
	return 1
}

// overlayHeight estimates the terminal rows the approval/ask boxes need so
// the viewport can yield room to them.
func (m *Model) overlayHeight() int {
	n := 0
	if len(m.pendingApproval) > 0 {
		n += len(m.pendingApproval) + 5 // title + hint + border + padding
	}
	if m.pendingAsk != nil && m.pendingAsk.qi < len(m.pendingAsk.questions) {
		n += len(m.pendingAsk.questions[m.pendingAsk.qi].Options) + 7
	}
	if m.pickerOpen {
		rows := len(m.picker.view)
		if rows > pickerMaxRows {
			rows = pickerMaxRows
		}
		n += rows + 4 // title + hint + border
	}
	if m.menuOpen {
		rows := len(m.menuItems)
		if rows > menuMaxRows {
			rows = menuMaxRows
		}
		n += rows + 3 // hint + border
	}
	if m.atOpen {
		rows := len(m.atItems)
		if rows > atMaxRows {
			rows = atMaxRows
		}
		n += rows + 3
	}
	if m.sessPicker.open {
		rows := len(m.sessPicker.view)
		if rows > sessionPickerMaxRows {
			rows = sessionPickerMaxRows
		}
		n += rows + 3
	}
	return n
}

// View implements tea.Model. The app renders in the terminal's alternate
// screen buffer so streaming frames never smear into the scrollback.
func (m Model) View() tea.View {
	if !m.ready {
		v := tea.NewView("Loading…")
		v.AltScreen = true
		return v
	}

	parts := []string{brandStyle.Render("✳ rakit")}
	if s := m.statusLabel(); s != "" {
		parts = append(parts, statusStyle.Render(s))
	}
	parts = append(parts,
		statusStyle.Render(m.modelLabel()),
		statusStyle.Render(m.workspaceLabel()),
	)
	if env := strings.TrimPrefix(m.envLabel(), "  ·  "); env != "" {
		parts = append(parts, statusStyle.Render(env))
	}
	header := strings.Join(parts, statusStyle.Render("  ·  "))

	body := m.viewport.View()
	if len(m.pendingApproval) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, body, m.renderApprovalBox())
	}
	if m.pendingAsk != nil {
		if box := m.renderAskBox(); box != "" {
			body = lipgloss.JoinVertical(lipgloss.Left, body, box)
		}
	}

	input := m.textarea.View()
	if sp := m.renderSessionPicker(); sp != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, sp, input)
	} else if picker := m.renderModelPicker(); picker != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, picker, input)
	} else if at := m.renderAtMenu(); at != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, at, input)
	} else if menu := m.renderSlashMenu(); menu != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, menu, input)
	}
	footer := borderStyle.Render(input)

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		body,
		m.activityLine(),
		footer,
		m.renderStatusBar(),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "rakit"
	return v
}

// statusLabel renders the working spinner with its rotating verb, or the
// plain status word.
func (m Model) statusLabel() string {
	// While a turn runs, the transcript owns the activity indicator (the
	// inline spinner before the first delta and the ✻ reasoning block after).
	// Keeping a second animated verb in the header read as duplicate
	// "thinking" noise.
	if m.busy {
		return ""
	}
	return m.status
}

// envLabel renders the cached MCP and skills counters for the header.
func (m Model) envLabel() string {
	if m.eng == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if m.mcpTotal > 0 {
		parts = append(parts, fmt.Sprintf("mcp %d/%d · %d tools", m.mcpUp, m.mcpTotal, m.mcpTools))
	}
	if m.skillCnt > 0 {
		parts = append(parts, fmt.Sprintf("skills %d", m.skillCnt))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  ·  " + strings.Join(parts, " · ")
}

// workspaceLabel shortens the workspace path for the header.
func (m Model) workspaceLabel() string {
	if m.eng == nil {
		return "no workspace"
	}
	ws := m.eng.Workspace()
	if ws == "" {
		return "no workspace"
	}
	for i := len(ws) - 1; i >= 0; i-- {
		if ws[i] == '/' {
			return ws[i+1:]
		}
	}
	return ws
}

// modelLabel returns the cached session model for the header, rendered with
// the profile's display name ("Kenari/model") when known.
func (m Model) modelLabel() string {
	if m.modelName == "" {
		return "unknown model"
	}
	return m.displayModelName(m.modelName)
}

// askState tracks progress through a batch of pending ask-tool questions.
type askState struct {
	questions []tools.AskQuestion
	qi        int
	// picks maps question id → selected options.
	picks map[string][]string
}

func newAskState(qs []tools.AskQuestion) *askState {
	return &askState{
		questions: append([]tools.AskQuestion(nil), qs...),
		picks:     map[string][]string{},
	}
}

// fingerprint identifies a question batch so new questions reset progress.
func (a *askState) fingerprint() string { return askFingerprint(a.questions) }

func askFingerprint(qs []tools.AskQuestion) string {
	ids := make([]string, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return strings.Join(ids, ",")
}

// eventPump batches engine bus events into bubbletea messages. Bus handlers
// run on the publisher's goroutine (the turn goroutine), so handlers only
// append to a pending queue and a timer flushes it to the program — Update
// never runs on the publisher.
//
// Streaming deltas are coalesced: consecutive deltas of the same type for
// the same session accumulate into one message per flush (~20/s), so a fast
// model re-renders the transcript tens of times per second instead of
// hundreds.
type eventPump struct {
	mu        sync.Mutex
	prog      *tea.Program
	stopped   bool
	unsubs    []func()
	pending   []pendingItem
	scheduled bool
}

// pumpFlushInterval is how often coalesced events land in the program.
const pumpFlushInterval = 50 * time.Millisecond

// pendingItem is either a coalesced delta run (ev == nil) or a passthrough
// event (ev != nil, in original order between the delta runs).
type pendingItem struct {
	typ  events.EventType // delta event type when ev == nil
	sid  string           // session_id of the coalesced run
	text string           // accumulated delta payload
	ev   *events.Event    // passthrough event
}

// startPump subscribes to every agent event type the UI renders.
func startPump(bus *events.Bus, prog *tea.Program) *eventPump {
	pmp := &eventPump{prog: prog}
	sub := func(t events.EventType) {
		pmp.unsubs = append(pmp.unsubs, bus.Subscribe(t, func(ev events.Event) {
			pmp.push(ev)
		}))
	}
	sub(events.AgentTurnStart)
	sub(events.AgentTurnEnd)
	sub(events.AgentMessageDelta)
	sub(events.AgentThinkingDelta)
	sub(events.AgentToolStart)
	sub(events.AgentToolEnd)
	sub(events.AgentAsk)
	sub(agentUpdatedEvent)
	return pmp
}

// push enqueues one event without blocking the publisher and schedules a
// flush if none is pending.
func (p *eventPump) push(ev events.Event) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	sid, _ := ev.Data["session_id"].(string)
	switch ev.Type {
	case events.AgentMessageDelta, events.AgentThinkingDelta:
		delta, _ := ev.Data["delta"].(string)
		if n := len(p.pending); n > 0 && p.pending[n-1].ev == nil &&
			p.pending[n-1].typ == ev.Type && p.pending[n-1].sid == sid {
			p.pending[n-1].text += delta
		} else {
			p.pending = append(p.pending, pendingItem{typ: ev.Type, sid: sid, text: delta})
		}
	default:
		evCopy := ev
		p.pending = append(p.pending, pendingItem{ev: &evCopy})
	}
	if !p.scheduled && p.prog != nil {
		p.scheduled = true
		time.AfterFunc(pumpFlushInterval, p.flush)
	}
	p.mu.Unlock()
}

// flush hands the coalesced queue to the program in original order.
func (p *eventPump) flush() {
	p.mu.Lock()
	p.scheduled = false
	items := p.pending
	p.pending = nil
	prog := p.prog
	stopped := p.stopped
	p.mu.Unlock()
	if prog == nil || stopped {
		return
	}
	for _, it := range items {
		if it.ev != nil {
			prog.Send(busEvent{ev: *it.ev})
			continue
		}
		data := map[string]any{"session_id": it.sid, "delta": it.text}
		if it.typ == events.AgentMessageDelta {
			data["kind"] = "text"
		}
		prog.Send(busEvent{ev: events.Event{Type: it.typ, Data: data}})
	}
}

// stop unsubscribes from the bus and drops anything still pending.
func (p *eventPump) stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()
	for _, u := range p.unsubs {
		u()
	}
}
