package agent

import (
	"fmt"
	"log"
	"sync"

	"github.com/Haslab-dev/RakitKode/events"
)

// Thrash-alarm thresholds. Deliberately lower than the LoopDetector circuit
// breaker (which stops the turn at >4 repeats): the alarm only nudges the
// model toward a different approach while the turn is still running.
const (
	ThrashConsecutiveFailures = 3
	ThrashRepeatCount         = 3
)

// ThrashWarningText is injected into the volatile system-reminder when the
// alarm fires. It prescribes the escape hatch (delegation or a different
// approach) instead of letting the model keep grinding.
const ThrashWarningText = "Loop detected: delegate to a subagent or change approach."

// thrashState tracks one session's tool-health signals for the thrash alarm:
// consecutive tool failures and identical tool+argument repetitions.
type thrashState struct {
	mu                  sync.Mutex
	consecutiveFailures int
	lastSig             string
	sigCount            int
	pending             string // warning not yet surfaced to the model
}

// record ingests one tool result. It returns the warning to surface this
// step, or "" when healthy. The warning fires once per episode: a successful
// call or a changed signature rearms it.
func (t *thrashState) record(tool, args string, isErr bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	sig := tool + "\x00" + args
	if sig == t.lastSig {
		t.sigCount++
	} else {
		t.lastSig = sig
		t.sigCount = 1
	}

	if isErr {
		t.consecutiveFailures++
	} else {
		t.consecutiveFailures = 0
		t.pending = "" // recovery clears any un-surfaced warning
	}

	if t.pending != "" {
		return "" // already alarming; don't spam every step
	}
	switch {
	case t.consecutiveFailures >= ThrashConsecutiveFailures:
		t.pending = fmt.Sprintf("%s (%d consecutive tool failures — last failing tool: %s).", ThrashWarningText, t.consecutiveFailures, tool)
	case t.sigCount >= ThrashRepeatCount:
		t.pending = fmt.Sprintf("%s (%s called %dx with identical arguments).", ThrashWarningText, tool, t.sigCount)
	default:
		return ""
	}
	return t.pending
}

// take returns and clears the pending warning (called once per step by the
// volatile-reminder builder).
func (t *thrashState) take() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.pending
	t.pending = ""
	return w
}

func (t *thrashState) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.consecutiveFailures = 0
	t.lastSig = ""
	t.sigCount = 0
	t.pending = ""
}

// thrashFor returns (creating if needed) the thrash state for a session.
func (m *Manager) thrashFor(sessionID string) *thrashState {
	m.thrashMu.Lock()
	defer m.thrashMu.Unlock()
	if m.thrashStates == nil {
		m.thrashStates = make(map[string]*thrashState)
	}
	st, ok := m.thrashStates[sessionID]
	if !ok {
		st = &thrashState{}
		m.thrashStates[sessionID] = st
	}
	return st
}

// recordThrash ingests one tool result into the alarm. On the first trigger
// of an episode it emits events.AgentLoopWarning so hosts (TUI, desktop app)
// can surface the struggle live.
func (m *Manager) recordThrash(sessionID, tool, args string, isErr bool) {
	warning := m.thrashFor(sessionID).record(tool, args, isErr)
	if warning == "" {
		return
	}
	log.Printf("[agent:%s] thrash alarm: %s", sessionID, warning)
	m.emitAgentEvent(events.AgentLoopWarning, sessionID, map[string]interface{}{
		"tool":    tool,
		"warning": warning,
	})
}

// takeThrashWarning drains the pending alarm line for the volatile
// system-reminder (one-shot per step).
func (m *Manager) takeThrashWarning(sessionID string) string {
	m.thrashMu.Lock()
	st, ok := m.thrashStates[sessionID]
	m.thrashMu.Unlock()
	if !ok {
		return ""
	}
	return st.take()
}

// resetThrash clears the alarm at turn boundaries.
func (m *Manager) resetThrash(sessionID string) {
	m.thrashMu.Lock()
	st, ok := m.thrashStates[sessionID]
	m.thrashMu.Unlock()
	if ok {
		st.reset()
	}
}
