// Terminal UI for rakit: a chat transcript backed by the event bus. Finished
// turns are printed to scrollback via tea.Println; only the streaming reply,
// the activity spinner, and the input box live in the redrawn region.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/events"
)

var (
	tuiUserStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	tuiToolStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tuiErrStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	tuiHintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tuiSpinStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	tuiSpinFrames   = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	tuiSpinInterval = 100 * time.Millisecond
)

type deltaMsg struct{ text string }
type toolMsg struct{ name string }
type turnDoneMsg struct{ err error }
type spinnerTickMsg struct{}

type tuiModel struct {
	e       *engine.Engine
	sessID  string
	timeout time.Duration
	input   textinput.Model

	cur      string // assistant reply currently streaming
	showTool string // tool announced but not yet followed by text
	busy     bool
	spin     int
	width    int
	quitting bool
}

func newTUIModel(e *engine.Engine, sessID string, timeout time.Duration) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "ask anything…"
	ti.Prompt = "❯ "
	ti.CharLimit = 0
	ti.Focus()
	return tuiModel{e: e, sessID: sessID, timeout: timeout, input: ti}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForTUIEvent)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.Width = max(20, msg.Width-4)
		return m, nil

	case spinnerTickMsg:
		if !m.busy {
			return m, nil
		}
		m.spin = (m.spin + 1) % len(tuiSpinFrames)
		return m, tickSpinner()

	case deltaMsg:
		m.cur += msg.text
		m.showTool = ""
		return m, waitForTUIEvent

	case toolMsg:
		m.showTool = msg.name
		return m, tea.Batch(printLine(tuiToolStyle.Render("  · "+msg.name)), waitForTUIEvent)

	case turnDoneMsg:
		m.busy = false
		var cmds []tea.Cmd
		if m.cur != "" {
			cmds = append(cmds, printLine(m.cur))
		}
		switch {
		case msg.err == nil:
		case errors.Is(msg.err, context.Canceled):
			cmds = append(cmds, printLine(tuiErrStyle.Render("  (interrupted)")))
		case errors.Is(msg.err, context.DeadlineExceeded):
			cmds = append(cmds, printLine(tuiErrStyle.Render("  (timed out)")))
		default:
			cmds = append(cmds, printLine(tuiErrStyle.Render("  error: "+msg.err.Error())))
		}
		m.cur = ""
		m.showTool = ""
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.busy {
				// First press interrupts the turn; second quits.
				m.e.Manager().StopTurn(m.sessID)
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.busy {
				return m, nil
			}
			m.input.SetValue("")
			m.busy = true
			m.input.Blur()
			return m, tea.Batch(
				printLine(tuiUserStyle.Render("❯ "+text)),
				runTUXTurn(m.e, m.sessID, text, m.timeout),
				tickSpinner(),
			)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m tuiModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	if m.busy {
		switch {
		case m.cur != "":
			b.WriteString(wrapTUI(m.cur, m.width))
		case m.showTool != "":
			b.WriteString(tuiSpinStyle.Render(tuiSpinFrames[m.spin]) + " " + tuiToolStyle.Render(m.showTool))
		default:
			b.WriteString(tuiSpinStyle.Render(tuiSpinFrames[m.spin]) + " thinking…")
		}
		b.WriteString("\n")
	} else if m.cur != "" {
		b.WriteString(wrapTUI(m.cur, m.width) + "\n")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n" + tuiHintStyle.Render("enter send · ctrl+c interrupt/quit"))
	return b.String()
}

func wrapTUI(s string, width int) string {
	if width < 20 {
		width = 20
	}
	return lipgloss.NewStyle().Width(width - 2).Render(s)
}

func printLine(s string) tea.Cmd {
	return tea.Println(s)
}

func tickSpinner() tea.Cmd {
	return tea.Tick(tuiSpinInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// runTUXTurn executes one turn and reports completion. Deltas arrive
// separately through the bus pump; only the terminal state comes back here.
func runTUXTurn(e *engine.Engine, sessionID, prompt string, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		_, err := e.RunIn(ctx, sessionID, prompt)
		return turnDoneMsg{err: err}
	}
}

// waitForTUIEvent re-arms the bus pump: each delivered message asks for the
// next one, keeping bubbletea in charge of scheduling. Only Init and the
// channel-sourced cases (deltaMsg, toolMsg) return it, so exactly one
// receiver is pending at any time.
func waitForTUIEvent() tea.Msg { return <-tuiEvents }

var tuiEvents chan tea.Msg

// pumpTUIEvents forwards engine bus events for the given session into the
// channel bubbletea polls. Sends never block the publisher: if the UI falls
// behind, cosmetic deltas are dropped.
func pumpTUIEvents(e *engine.Engine, sessID string) {
	tuiEvents = make(chan tea.Msg, 256)
	sub := func(t events.EventType, mk func(events.Event) tea.Msg) {
		e.Bus().Subscribe(t, func(ev events.Event) {
			if id, _ := ev.Data["session_id"].(string); id != "" && id != sessID {
				return
			}
			msg := mk(ev)
			if msg == nil {
				return
			}
			select {
			case tuiEvents <- msg:
			default:
			}
		})
	}
	sub(events.AgentMessageDelta, func(ev events.Event) tea.Msg {
		if kind, _ := ev.Data["kind"].(string); kind != "text" {
			return nil
		}
		delta, _ := ev.Data["delta"].(string)
		return deltaMsg{text: delta}
	})
	sub(events.AgentToolStart, func(ev events.Event) tea.Msg {
		name, _ := ev.Data["name"].(string)
		return toolMsg{name: name}
	})
}

// cmdTUI runs the interactive terminal UI.
func cmdTUI(args []string) int {
	f, _ := parseRunFlags(args)
	if f == nil {
		return 2
	}
	e, err := newEngine(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	if !e.HasProvider() {
		fmt.Fprintln(os.Stderr, "rakit:", engine.ErrNoProvider)
		return 1
	}

	sessID := f.session
	if sessID == "" && f.cont {
		sessID = latestSessionID(e)
		if sessID == "" {
			fmt.Fprintln(os.Stderr, "rakit: no previous session to continue")
			return 1
		}
	}
	if sessID == "" {
		sess, err := e.Manager().CreateSession("rakit", agent.RoleCoding, e.Workspace())
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		sessID = sess.ID
	}
	if f.yolo {
		_ = e.Manager().SetAutoApprove(sessID, true)
	}

	pumpTUIEvents(e, sessID)
	m := newTUIModel(e, sessID, f.timeout)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	return 0
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
