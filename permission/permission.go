// Package permission implements the tool permission pipeline: collaboration
// modes, allow/deny/ask rules and per-tool decisions. It is the live engine
// counterpart of the ported ruleset in harness/permission.go.
package permission

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Mode is the collaboration mode for a session.
type Mode string

const (
	ModePlan  Mode = "plan"
	ModeBuild Mode = "build"
	ModeEdit  Mode = "edit"
	ModeYolo  Mode = "yolo"
	ModeAuto  Mode = "auto"
)

// Action is the result of a permission check.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)

// Rule matches a tool and optionally a content subject (command, path, url…).
type Rule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern,omitempty"`
}

// UnmarshalJSON accepts either "Bash(git push:*)" or
// {"tool":"Bash","pattern":"git push:*}.
func (r *Rule) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*r = ParseRule(s)
		return nil
	}
	type alias Rule
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = Rule(a)
	return nil
}

// MarshalJSON renders a rule back to the compact string form.
func (r Rule) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

func (r Rule) String() string {
	if r.Pattern == "" {
		return r.Tool
	}
	return fmt.Sprintf("%s(%s)", r.Tool, r.Pattern)
}

// ParseRule parses "Bash(git push:*)" / "Edit" into a Rule.
func ParseRule(s string) Rule {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open <= 0 || !strings.HasSuffix(s, ")") {
		return Rule{Tool: s}
	}
	return Rule{Tool: strings.TrimSpace(s[:open]), Pattern: s[open+1 : len(s)-1]}
}

// Ruleset groups allow/deny/ask rules.
type Ruleset struct {
	Allow []Rule `json:"allow,omitempty"`
	Deny  []Rule `json:"deny,omitempty"`
	Ask   []Rule `json:"ask,omitempty"`
}

// Settings is the on-disk permission configuration.
type Settings struct {
	Mode            Mode     `json:"mode,omitempty"`
	Permissions     Ruleset  `json:"permissions"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
}

// ToolMeta is the subset of tool metadata the permission pipeline needs.
type ToolMeta struct {
	Name                    string
	ReadOnly                bool
	Destructive             bool
	NeedsApproval           bool
	RequiresUserInteraction bool
	SideEffectScope         string
	IsMCP                   bool
}

// Context is the per-session permission state.
type Context struct {
	Mode         Mode
	PlanEnabled  bool
	Rules        Ruleset
	SessionAllow []Rule
	Disallowed   []string
}

// Decision is the pipeline result for one tool call.
type Decision struct {
	Action    Action
	Message   string
	AlwaysAsk bool
}

// Check evaluates one tool call. Ordering mirrors the reference pipeline:
// plan transitions, user interaction, approval capability, yolo, auto,
// disallowed, project deny/ask, plan restriction, project allow, mode rules.
func Check(tool string, meta ToolMeta, input map[string]any, s Context) Decision {
	if tool == "EnterPlanMode" || tool == "enter_plan_mode" {
		return Decision{Action: ActionAllow}
	}
	if (tool == "ExitPlanMode" || tool == "exit_plan_mode") && !s.PlanEnabled && s.Mode != ModePlan {
		return Decision{Action: ActionDeny, Message: "plan mode not active"}
	}

	// Hard denials always win, in every mode.
	if contains(s.Disallowed, tool) {
		return Decision{Action: ActionDeny, Message: "tool is disallowed"}
	}
	if _, ok := MatchRules(s.Rules.Deny, tool, input); ok {
		return Decision{Action: ActionDeny, Message: "denied by project rule"}
	}

	if meta.RequiresUserInteraction {
		// The `ask` tool is itself the user-interaction surface; it must run
		// so it can pause the session and present the question.
		if tool == "ask" {
			return Decision{Action: ActionAllow}
		}
		if s.Mode == ModeAuto {
			return Decision{Action: ActionDeny, Message: "mode.auto requires explicit approval"}
		}
		return Decision{Action: ActionAsk}
	}

	// Plan mode: read-only (and non-destructive MCP / session-scoped) tools only.
	if s.PlanEnabled || s.Mode == ModePlan {
		if meta.ReadOnly && !meta.Destructive {
			return Decision{Action: ActionAllow}
		}
		if meta.SideEffectScope == "session" && !meta.Destructive {
			return Decision{Action: ActionAllow}
		}
		if meta.IsMCP && !meta.Destructive {
			return Decision{Action: ActionAllow}
		}
		return Decision{Action: ActionDeny, Message: "plan mode allows read-only tools only"}
	}

	if s.Mode == ModeYolo {
		return Decision{Action: ActionAllow}
	}
	if s.Mode == ModeAuto {
		if meta.ReadOnly && !meta.Destructive {
			return Decision{Action: ActionAllow}
		}
		return Decision{Action: ActionDeny, Message: "mode.auto requires explicit approval"}
	}

	if _, ok := MatchRules(s.SessionAllow, tool, input); ok {
		return Decision{Action: ActionAllow}
	}
	if _, ok := MatchRules(s.Rules.Ask, tool, input); ok {
		return Decision{Action: ActionAsk}
	}
	if _, ok := MatchRules(s.Rules.Allow, tool, input); ok {
		return Decision{Action: ActionAllow}
	}

	if meta.ReadOnly && !meta.Destructive {
		return Decision{Action: ActionAllow}
	}

	if s.Mode == ModeEdit {
		switch tool {
		case "Write", "Edit", "MultiEdit", "NotebookEdit", "write", "edit", "multiedit":
			return Decision{Action: ActionAllow}
		}
	}
	if meta.NeedsApproval || meta.Destructive || meta.SideEffectScope == "workspace" {
		return Decision{Action: ActionAsk, AlwaysAsk: meta.NeedsApproval}
	}
	return Decision{Action: ActionAllow}
}

// MatchRules returns the first matching rule.
func MatchRules(rules []Rule, tool string, input map[string]any) (Rule, bool) {
	for _, r := range rules {
		if !toolMatches(r.Tool, tool) {
			continue
		}
		if r.Pattern == "" {
			return r, true
		}
		subject := subjectOf(input)
		if subject == "" {
			continue
		}
		if contentMatches(r.Pattern, subject) {
			return r, true
		}
	}
	return Rule{}, false
}

func toolMatches(ruleTool, tool string) bool {
	if strings.EqualFold(ruleTool, tool) {
		return true
	}
	// An Edit rule also covers Write, matching the reference behaviour.
	if strings.EqualFold(ruleTool, "Edit") && strings.EqualFold(tool, "Write") {
		return true
	}
	return false
}

func subjectOf(input map[string]any) string {
	for _, k := range []string{"command", "url", "file_path", "path", "pattern", "patch_text", "cmd"} {
		if v, ok := input[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func contentMatches(rule, subject string) bool {
	if strings.HasSuffix(rule, ":*") {
		prefix := strings.TrimSuffix(rule, ":*")
		return subject == prefix || strings.HasPrefix(subject, prefix+" ") || strings.HasPrefix(subject, prefix+"\t")
	}
	if strings.HasSuffix(rule, "**") {
		prefix := strings.TrimSuffix(rule, "**")
		if strings.HasSuffix(prefix, "/") {
			prefix = strings.TrimSuffix(prefix, "/")
		}
		return subject == prefix || strings.HasPrefix(subject, prefix+"/") || strings.HasPrefix(subject, prefix)
	}
	if strings.Contains(rule, "*") {
		pattern := regexp.QuoteMeta(rule)
		pattern = strings.ReplaceAll(pattern, `\*`, ".*")
		ok, err := regexp.MatchString("^"+pattern+"$", subject)
		return err == nil && ok
	}
	return rule == subject
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// LoadSettings reads settings files in order and merges them. Missing files
// are ignored; later files override mode and extend the rule lists.
func LoadSettings(paths ...string) (Settings, error) {
	var out Settings
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return out, err
		}
		var s Settings
		if err := json.Unmarshal(data, &s); err != nil {
			return out, fmt.Errorf("parse %s: %w", p, err)
		}
		if s.Mode != "" {
			out.Mode = s.Mode
		}
		out.Permissions.Allow = append(out.Permissions.Allow, s.Permissions.Allow...)
		out.Permissions.Deny = append(out.Permissions.Deny, s.Permissions.Deny...)
		out.Permissions.Ask = append(out.Permissions.Ask, s.Permissions.Ask...)
		out.DisallowedTools = append(out.DisallowedTools, s.DisallowedTools...)
	}
	return out, nil
}

// DefaultSettingsPaths returns the global and workspace settings paths in
// merge order.
func DefaultSettingsPaths(home, workspace string) []string {
	var paths []string
	if home != "" {
		paths = append(paths, filepath.Join(home, "settings.json"))
	}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".rakit", "settings.json"))
	}
	return paths
}
