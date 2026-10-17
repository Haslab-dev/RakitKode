// Package tui implements the interactive terminal UI for the rakit engine:
// a streaming transcript viewport keyed by turn, a multi-line input, a slash
// command menu, an inline tool-approval prompt, and structured ask rendering.
//
// Engine events arrive on the event bus from the turn goroutine; the pump
// marshals them into bubbletea messages through a channel so Update never
// runs on the publisher's goroutine.
package tui

import (
	"context"
	"strings"
	"sync"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/tools"
)

// agentUpdatedEvent is published by the manager as a raw string (there is no
// exported const in the events package yet).
var agentUpdatedEvent = events.EventType("agent:updated")

// statusWorking is the spinner label shown while a turn runs.
const statusWorking = "◍ working…"

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
	// toolLines maps tool call ids to transcript indexes.
	toolLines map[string]int

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

	// /model picker state (interactive; esc returns to the input)
	pickerOpen bool
	picker     modelPicker

	// usage counters pulled from the session snapshot
	tokensIn  int
	tokensOut int
	cacheHit  float64

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
	ta.Placeholder = "Ask anything…  (/ commands · shift+enter newline)"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 8
	ta.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(spinnerStyle))

	m := Model{
		eng:       e,
		sessionID: sessionID,
		textarea:  ta,
		spinner:   sp,
		viewport:  viewport.New(),
		status:    "ready",
		cur:       streamBlocks{reasoning: -1, text: -1},
		toolLines: map[string]int{},
	}
	if e != nil {
		m.mgr = e.Manager()
	}
	m.pullSession()
	m.appendSessionHeader()
	return m
}

// Run executes the TUI program for one session: it subscribes to the engine
// event bus, runs the bubbletea loop, and blocks until the user quits.
func Run(e *engine.Engine, sessionID string) error {
	m := New(e, sessionID)
	p := tea.NewProgram(m)
	pump := startPump(e.Bus(), sessionID)
	defer pump.stop()
	go feedProgram(pump, p)
	_, err := p.Run()
	return err
}

// feedProgram forwards pumped events to the running program until stopped.
func feedProgram(pump *eventPump, p *tea.Program) {
	for msg := range pump.ch {
		if pump.isStopped() {
			return
		}
		p.Send(msg)
	}
}

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

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds := []tea.Cmd{cmd}
		if m.busy {
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		if handled, cmd := m.interceptKey(msg); handled {
			return m, cmd
		}

	case busEvent:
		m.handleBusEvent(msg.ev)
		return m, nil
	}

	// Feed the textarea; live-trigger the slash menu on "/" input.
	heightBefore := m.textarea.Height()
	valueBefore := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds := []tea.Cmd{cmd}
	valueAfter := m.textarea.Value()
	switch {
	case strings.HasPrefix(valueAfter, "/model"):
		// /model drives the interactive picker, not the command menu.
		// Prefix match: typed characters after "/model" (with or without a
		// space) filter the list instead of closing it.
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
	if m.textarea.Height() != heightBefore {
		m.syncLayout()
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
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
			m.appendBlock(toolStyle.Render("  → granted"))
		case "a":
			m.respondApproval(true, true)
			m.appendBlock(toolStyle.Render("  → granted (auto-approve on)"))
		case "n":
			m.respondApproval(false, false)
			m.appendBlock(toolStyle.Render("  → denied"))
		}
		return true, nil
	}

	// Structured ask: number keys pick options for the current question.
	if m.pendingAsk != nil {
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
				m.appendBlock(userStyle.Render("❯ /model " + model))
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

	switch key {
	case "tab":
		// Toggle the most recent collapsed reasoning block.
		m.toggleReasoning()
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
	case "alt+enter", "ctrl+j":
		// Newline inside the draft; enter still submits.
		m.textarea.InsertString("\n")
		return true, nil
	}
	return false, nil
}

// submit sends the current input: slash commands run inline, anything else
// starts one agent turn via the manager.
func (m *Model) submit() tea.Cmd {
	value := strings.TrimSpace(m.textarea.Value())
	m.textarea.Reset()
	if value == "" || m.busy {
		return nil
	}
	m.menuOpen = false
	m.appendBlock(userStyle.Render("❯ ") + value)
	if strings.HasPrefix(value, "/") {
		return m.runSlashInput(value)
	}
	m.busy = true
	m.cancelRequested = false
	m.status = statusWorking
	if m.mgr == nil {
		m.busy = false
		m.status = "ready"
		m.appendBlock(errorStyle.Render("error: no engine attached"))
		return nil
	}
	if err := m.mgr.SendMessage(context.Background(), m.sessionID, value, nil); err != nil {
		m.busy = false
		m.status = "ready"
		m.appendBlock(errorStyle.Render("error: " + err.Error()))
	}
	return nil
}

// runSlashInput interprets a "/..." submission already echoed to the transcript.
func (m *Model) runSlashInput(value string) tea.Cmd {
	for _, line := range m.handleSlashInput(value) {
		m.appendBlock(line)
	}
	if m.quitRequested {
		return tea.Quit
	}
	return nil
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
	case events.AgentTurnStart:
		m.closeStream()
		m.busy = true
		m.status = statusWorking
	case events.AgentTurnEnd:
		m.closeStream()
		m.busy = false
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
	m.cacheHit = sess.CacheHitRate
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

// statusForBusy returns the spinner label.
func statusForBusy(busy bool) string {
	if busy {
		return statusWorking
	}
	return "ready"
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

	h := m.height - m.taLines() - 8 // header 1 + input border 2 + status 1 + breathing room
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

	header := brandStyle.Render("✳ Rakit") +
		statusStyle.Render("  ·  "+m.statusLabel()) +
		statusStyle.Render("  ·  "+m.modelLabel()) +
		statusStyle.Render("  ·  "+m.workspaceLabel())

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
	if picker := m.renderModelPicker(); picker != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, picker, input)
	} else if menu := m.renderSlashMenu(); menu != "" {
		input = lipgloss.JoinVertical(lipgloss.Left, menu, input)
	}
	footer := borderStyle.Render(input)

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		body,
		footer,
		m.renderStatusBar(),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "rakit"
	return v
}

// statusLabel renders the working spinner or the plain status word.
func (m Model) statusLabel() string {
	if m.busy && m.status == statusWorking {
		return m.spinner.View() + " working…"
	}
	return m.status
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

// modelLabel returns the cached session model for the header.
func (m Model) modelLabel() string {
	if m.modelName == "" {
		return "unknown model"
	}
	return m.modelName
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

// eventPump marshals engine bus events into bubbletea messages. Bus handlers
// run on the publisher's goroutine (the turn goroutine), so handlers only
// push into a buffered channel and a feeder goroutine hands the messages to
// the program — Update never runs on the publisher.
type eventPump struct {
	ch      chan tea.Msg
	mu      sync.Mutex
	stopped bool
	unsubs  []func()
}

// startPump subscribes to every agent event type the UI renders.
func startPump(bus *events.Bus, sessionID string) *eventPump {
	pmp := &eventPump{ch: make(chan tea.Msg, 512)}
	sub := func(t events.EventType) {
		pmp.unsubs = append(pmp.unsubs, bus.Subscribe(t, func(ev events.Event) {
			pmp.push(busEvent{ev: ev})
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

// push enqueues an event without ever blocking the publisher; when the UI
// falls behind, cosmetic deltas are dropped.
func (p *eventPump) push(msg tea.Msg) {
	if p.isStopped() {
		return
	}
	select {
	case p.ch <- msg:
	default:
	}
}

func (p *eventPump) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

// stop unsubscribes from the bus. The channel is deliberately never closed:
// a push racing with stop would panic on a closed channel, and the parked
// feeder goroutine is collected when the process exits.
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
