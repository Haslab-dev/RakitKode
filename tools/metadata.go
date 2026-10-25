package tools

import (
	"strings"
	"time"
)

// Default timeouts for tool categories.
const (
	DefaultToolTimeout = 120 * time.Second
	ShellToolTimeout   = 300 * time.Second
	MCPToolTimeout     = 300 * time.Second
)

// Tool classification metadata. It lives in one table so the scheduler, the
// permission pipeline and the agent loop cannot drift apart.
type ToolMetadata struct {
	ReadOnly                bool
	Destructive             bool
	ConcurrencySafe         *bool
	NeedsApproval           bool
	RequiresUserInteraction bool
	SideEffectScope         string // none|session|workspace
	Timeout                 time.Duration
	MaxOutputBytes          int
	Category                string // file|shell|search|web|skill|plugin|task|mcp|plan
}

// MetadataFor returns the classification for a tool name. Unknown tools are
// treated as workspace-mutating and requiring approval (fail closed).
func MetadataFor(name string) ToolMetadata {
	n := strings.ToLower(name)
	switch n {
	case "read", "read_file", "view_file", "cat", "ls", "list_dir", "read_multiple",
		"read_directory_files", "search", "search_workspace", "rg", "grep", "ripgrep",
		"find", "glob", "index", "git_status", "diff", "list_skills", "list_plugins",
		"list_subagents", "get_subagent_output", "list_schedules", "task_output",
		"task_list", "recall_memory", "load_skill", "skill", "call_skill", "run_skill",
		"invoke_skill":
		return ToolMetadata{ReadOnly: true, SideEffectScope: "none", Timeout: DefaultToolTimeout, Category: "search"}
	case "web_fetch", "fetch", "http_get", "read_url", "web_search", "search_web", "google", "ddg":
		return ToolMetadata{ReadOnly: true, SideEffectScope: "none", Timeout: DefaultToolTimeout, Category: "web"}
	case "ask":
		return ToolMetadata{ReadOnly: true, RequiresUserInteraction: true, SideEffectScope: "none", Category: "task"}
	case "write", "write_file", "create_file", "edit", "edit_file", "replace_file_content",
		"multiedit", "multi_edit":
		return ToolMetadata{NeedsApproval: true, SideEffectScope: "workspace", Timeout: DefaultToolTimeout, Category: "file"}
	case "bash", "run_shell", "exec", "run_command", "background_shell":
		return ToolMetadata{Destructive: true, NeedsApproval: true, SideEffectScope: "workspace", Timeout: ShellToolTimeout, MaxOutputBytes: 100 << 10, Category: "shell"}
	case "python_eval", "python", "eval", "run_code":
		return ToolMetadata{Destructive: true, NeedsApproval: true, SideEffectScope: "workspace", Timeout: ShellToolTimeout, Category: "shell"}
	case "todo", "update_tasks", "task", "invoke_subagent", "select_subagent",
		"schedule", "cancel_schedule", "enter_plan_mode", "exit_plan_mode":
		return ToolMetadata{SideEffectScope: "session", Timeout: DefaultToolTimeout, Category: "task"}
	case "create_skill", "create_plugin", "register_plugin", "toggle_plugin":
		return ToolMetadata{NeedsApproval: true, SideEffectScope: "workspace", Timeout: DefaultToolTimeout, Category: "plugin"}
	case "learn_memory":
		return ToolMetadata{SideEffectScope: "session", Timeout: DefaultToolTimeout, Category: "task"}
	}
	// MCP tools are registered as server/tool or mcp__server__tool.
	if strings.Contains(name, "/") || strings.HasPrefix(n, "mcp__") {
		return ToolMetadata{NeedsApproval: true, SideEffectScope: "workspace", Timeout: MCPToolTimeout, Category: "mcp"}
	}
	return ToolMetadata{NeedsApproval: true, SideEffectScope: "workspace", Timeout: DefaultToolTimeout}
}

// applyMetadata fills unset classification fields on a spec.
func applyMetadata(spec *ToolSpec) {
	md := MetadataFor(spec.Name)
	if !spec.ReadOnly {
		spec.ReadOnly = md.ReadOnly
	}
	if !spec.Destructive {
		spec.Destructive = md.Destructive
	}
	if spec.ConcurrencySafe == nil {
		spec.ConcurrencySafe = md.ConcurrencySafe
	}
	if !spec.NeedsApproval {
		spec.NeedsApproval = md.NeedsApproval
	}
	if !spec.RequiresUserInteraction {
		spec.RequiresUserInteraction = md.RequiresUserInteraction
	}
	if spec.SideEffectScope == "" {
		spec.SideEffectScope = md.SideEffectScope
	}
	if spec.Timeout == 0 {
		spec.Timeout = md.Timeout
	}
	if spec.MaxOutputBytes == 0 {
		spec.MaxOutputBytes = md.MaxOutputBytes
	}
	if spec.Category == "" {
		spec.Category = md.Category
	}
}

// MetaForSpec converts a ToolSpec to metadata (used by the permission gate).
func MetaForSpec(spec ToolSpec) ToolMetadata {
	return ToolMetadata{
		ReadOnly:                spec.ReadOnly,
		Destructive:             spec.Destructive,
		ConcurrencySafe:         spec.ConcurrencySafe,
		NeedsApproval:           spec.NeedsApproval,
		RequiresUserInteraction: spec.RequiresUserInteraction,
		SideEffectScope:         spec.SideEffectScope,
		Timeout:                 spec.Timeout,
		MaxOutputBytes:          spec.MaxOutputBytes,
		Category:                spec.Category,
	}
}
