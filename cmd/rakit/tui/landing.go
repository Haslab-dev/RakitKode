package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	landingTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#818CF8")) // Indigo 400

	landingTaglineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9CA3AF")) // Gray 400

	landingBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#6366F1")). // Indigo 500
			Padding(0, 1)

	landingSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#C7D2FE")) // Indigo 200

	landingCmdStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#38BDF8")) // Sky 400

	landingKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#E0E7FF")).
			Background(lipgloss.Color("#312E81")). // Indigo 900
			Padding(0, 1)

	landingTipStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FCD34D")) // Amber 300
)

// landingText returns a compact plain-text representation used for headless
// tests and non-TTY fallback.
func (m Model) landingText() string {
	return "❖ RAKITKODE  Autonomous AI Coding Agent\n" +
		"  /help commands · /model switch model · /new fresh session · /context token budget · ctrl+c quit"
}

// renderLandingPage builds a modern, responsive welcome screen for fresh sessions.
func (m Model) renderLandingPage() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	mode := "build"
	if m.eng != nil {
		mode = m.eng.PermissionModeFor(m.sessionID)
	}

	// Compact mode for narrow terminals (< 65 cols)
	if width < 65 {
		var sb strings.Builder
		sb.WriteString(landingTitleStyle.Render("❖ RAKITKODE") + " " + landingTaglineStyle.Render("· AI Coding Harness") + "\n\n")
		sb.WriteString(statusStyle.Render("⌂ ") + m.workspaceLabel() + "\n")
		sb.WriteString(statusStyle.Render("⬡ ") + m.modelLabel() + " · " + mode + "\n\n")
		sb.WriteString(landingCmdStyle.Render("/help") + statusStyle.Render("   list commands & tools\n"))
		sb.WriteString(landingCmdStyle.Render("/model") + statusStyle.Render("  switch model\n"))
		sb.WriteString(landingCmdStyle.Render("/new") + statusStyle.Render("    start fresh session\n"))
		sb.WriteString(landingCmdStyle.Render("/cost") + statusStyle.Render("   token economics\n\n"))
		sb.WriteString(landingTipStyle.Render("✦ Type your question or prompt to begin."))
		return sb.String()
	}

	// Full landing screen with information card
	cardWidth := width - 6
	if cardWidth > 74 {
		cardWidth = 74
	}

	modeDesc := "prompt before mutations & shell commands"
	switch strings.ToLower(mode) {
	case "yolo":
		modeDesc = "auto-approve all safe tools"
	case "edit":
		modeDesc = "direct file editing without confirmation"
	case "plan":
		modeDesc = "read-only exploration and planning"
	}

	cardLines := []string{
		landingTitleStyle.Render("❖ RAKITKODE") + "  " + landingTaglineStyle.Render("Autonomous AI Coding Agent & Harness"),
		"",
		fmt.Sprintf("  %-14s %s", statusStyle.Render("Directory:"), m.workspaceLabel()),
		fmt.Sprintf("  %-14s %s", statusStyle.Render("Active Model:"), m.modelLabel()),
		fmt.Sprintf("  %-14s %s %s", statusStyle.Render("Permission:"), strings.ToUpper(mode), statusStyle.Render("("+modeDesc+")")),
	}
	card := landingBoxStyle.Width(cardWidth).Render(strings.Join(cardLines, "\n"))

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(card)
	sb.WriteString("\n\n")

	sb.WriteString("  " + landingSectionStyle.Render("Quick Commands") + "\n")
	cmds := []struct{ cmd, desc string }{
		{"/help", "List available slash commands & agent capabilities"},
		{"/model", "Switch LLM model and provider interactively"},
		{"/new", "Start a fresh session and clear context"},
		{"/context", "Context window allocation and prompt cache status"},
		{"/cost", "Calculate session token economics & cost savings"},
	}
	for _, c := range cmds {
		sb.WriteString(fmt.Sprintf("    %-12s %s\n", landingCmdStyle.Render(c.cmd), statusStyle.Render(c.desc)))
	}

	sb.WriteString("\n  " + landingSectionStyle.Render("Keybindings") + "\n")
	sb.WriteString(fmt.Sprintf("    %s %s   %s %s   %s %s\n",
		landingKeyStyle.Render("Tab"), statusStyle.Render("Reasoning"),
		landingKeyStyle.Render("Ctrl+O"), statusStyle.Render("Tool outputs"),
		landingKeyStyle.Render("Ctrl+C"), statusStyle.Render("Cancel turn"),
	))

	sb.WriteString("\n  " + landingTipStyle.Render("✦ Start typing below to begin, or type / for all commands.") + "\n")

	return sb.String()
}
