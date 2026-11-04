package harness

import (
	"fmt"
	"runtime"
	"strings"
)

// SystemPromptBuilder assembles the system prompt in section order:
// identity → memory → project rules → env info → context management,
// with skills and dates as meta-user system-reminders.
type SystemPromptBuilder struct {
	Identity             string // "You are ForgeADE..."
	CustomPrompt         string // custom system prompt replaces identity body
	Memory               string
	WorkspaceDir         string
	GitStatus            string
	Platform             string
	OSVersion            string
	CurrentDate          string
	SkillsListing        string // rendered by SkillsContextSection
	ProjectRules         string // Unified rules (AGENTS.md, CLAUDE.md, .cursorrules)
	AutoDiscoverRules    bool   // Automatically discover and load project rules from WorkspaceDir
	ThinkingInstructions string // CoT / extended thinking instructions (DeepSeek-R1, Claude 3.7)
}

func (b SystemPromptBuilder) Build() string {
	if b.CustomPrompt != "" {
		return b.CustomPrompt
	}
	var s strings.Builder
	identity := b.Identity
	if identity == "" {
		identity = "You are ForgeADE, an interactive coding agent that helps users with software engineering tasks."
	}
	s.WriteString(identity + "\n\n")

	if b.ThinkingInstructions != "" {
		s.WriteString("# Reasoning & Internal Planning\n" + b.ThinkingInstructions + "\n\n")
	}

	if b.Memory != "" {
		s.WriteString("# Memory\n\n" + b.Memory + "\n\n")
	}

	// Project rules
	rules := b.ProjectRules
	if rules == "" && b.AutoDiscoverRules && b.WorkspaceDir != "" {
		_, rules = DiscoverWorkspaceRules(b.WorkspaceDir)
	}
	if rules != "" {
		s.WriteString(rules + "\n\n")
	}

	platform := b.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	fmt.Fprintf(&s, "# Environment\n- Working directory: %s\n- Platform: %s\n", b.WorkspaceDir, platform)
	if b.OSVersion != "" {
		fmt.Fprintf(&s, "- OS version: %s\n", b.OSVersion)
	}
	if b.GitStatus != "" {
		s.WriteString("- Git status: " + b.GitStatus + "\n")
	}
	s.WriteString("\n")

	if b.CurrentDate != "" {
		fmt.Fprintf(&s, "# Current date\n%s\n\n", b.CurrentDate)
	}

	s.WriteString("# Context management\nWhen the conversation grows long, older tool results may be compacted; the most recent context is always preserved. Read files again if their contents were cleared.\n")
	return s.String()
}

// MetaUserAttachments returns the meta-user attachments injected after the
// system messages: workspace instructions then the skills listing then date.
func (b SystemPromptBuilder) MetaUserAttachments() []string {
	var out []string
	if b.SkillsListing != "" {
		out = append(out, systemReminder(b.SkillsListing))
	}
	if b.CurrentDate != "" {
		out = append(out, systemReminder("Today's date is "+b.CurrentDate+"."))
	}
	return out
}
