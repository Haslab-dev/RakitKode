// Package hooks executes user-configured lifecycle hooks (PreToolUse,
// PostToolUse, UserPromptSubmit, Stop, SessionStart, SessionEnd, PreCompact).
//
// Hook configuration is Claude-Code compatible and merged global → workspace:
//
//	{"hooks": {
//	  "PreToolUse": [{"matcher": "Bash|Edit", "hooks": [{"type": "command",
//	    "command": ".rakit/hooks/guard.sh", "timeout": 30}]}]
//	}}
//
// A hook receives a JSON payload on stdin plus RAKIT_* environment variables.
// Exit code 2 blocks the action with stderr as the reason. JSON on stdout can
// return {"decision":"block","reason":"…","modified_input":{…},
// "system_message":"…","output":"…"}.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Event names.
type Event string

const (
	PreToolUse       Event = "PreToolUse"
	PostToolUse      Event = "PostToolUse"
	UserPromptSubmit Event = "UserPromptSubmit"
	Stop             Event = "Stop"
	SessionStart     Event = "SessionStart"
	SessionEnd       Event = "SessionEnd"
	PreCompact       Event = "PreCompact"
)

// Command is one hook entry.
type Command struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds, default 30
}

// Matcher groups hooks under a tool-name regex.
type Matcher struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []Command `json:"hooks"`
}

// Config maps an event to its matchers.
type Config map[string][]Matcher

// FileConfig is the on-disk shape.
type FileConfig struct {
	Hooks Config `json:"hooks"`
}

// Result is the aggregated outcome of a hook event.
type Result struct {
	Blocked       bool
	Reason        string
	ModifiedInput map[string]any
	Output        string
	SystemMessage string
}

type hookResponse struct {
	Decision      string         `json:"decision"`
	Reason        string         `json:"reason"`
	ModifiedInput map[string]any `json:"modified_input"`
	SystemMessage string         `json:"system_message"`
	Output        string         `json:"output"`
}

// Runner executes hooks for one workspace.
type Runner struct {
	config    Config
	workspace string
	// Timeout is the fallback when a hook does not set one.
	Timeout time.Duration
}

// NewRunner builds a runner. A nil config is valid and disables hooks.
func NewRunner(cfg Config, workspace string) *Runner {
	return &Runner{config: cfg, workspace: workspace, Timeout: 30 * time.Second}
}

// Load merges hook files in order. Missing files are ignored.
func Load(paths ...string) (Config, error) {
	out := Config{}
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
		var fc FileConfig
		if err := json.Unmarshal(data, &fc); err != nil {
			return out, fmt.Errorf("parse %s: %w", p, err)
		}
		for event, matchers := range fc.Hooks {
			out[event] = append(out[event], matchers...)
		}
	}
	return out, nil
}

// DefaultPaths returns global and workspace hook config paths.
func DefaultPaths(home, workspace string) []string {
	var paths []string
	if home != "" {
		paths = append(paths, filepath.Join(home, "hooks.json"))
	}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".rakit", "hooks.json"))
	}
	return paths
}

// Has reports whether any hooks are configured for the event.
func (r *Runner) Has(event Event) bool {
	if r == nil {
		return false
	}
	return len(r.config[string(event)]) > 0
}

// Run executes all matching hooks for the event. toolName is used for the
// matcher (empty for non-tool events). payload is sent on stdin.
func (r *Runner) Run(ctx context.Context, event Event, toolName string, payload map[string]any) Result {
	var result Result
	if r == nil || len(r.config[string(event)]) == 0 {
		return result
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["hook_event"] = string(event)
	payload["workspace"] = r.workspace
	if toolName != "" {
		payload["tool_name"] = toolName
	}

	for _, m := range r.config[string(event)] {
		if !matchTool(m.Matcher, toolName) {
			continue
		}
		for _, h := range m.Hooks {
			if h.Type != "" && h.Type != "command" {
				continue
			}
			res, blocked, reason := r.runOne(ctx, h, payload)
			if res.ModifiedInput != nil {
				result.ModifiedInput = res.ModifiedInput
				for k, v := range res.ModifiedInput {
					payload["tool_input_modified_"+k] = v
				}
			}
			if res.Output != "" {
				if result.Output != "" {
					result.Output += "\n"
				}
				result.Output += res.Output
			}
			if res.SystemMessage != "" {
				if result.SystemMessage != "" {
					result.SystemMessage += "\n"
				}
				result.SystemMessage += res.SystemMessage
			}
			if blocked {
				result.Blocked = true
				result.Reason = reason
				return result
			}
		}
	}
	return result
}

func (r *Runner) runOne(ctx context.Context, h Command, payload map[string]any) (hookResponse, bool, string) {
	timeout := time.Duration(h.Timeout) * time.Second
	if timeout <= 0 {
		timeout = r.Timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", h.Command)
	cmd.Dir = r.workspace
	input, _ := json.Marshal(payload)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"RAKIT_EVENT="+fmt.Sprint(payload["hook_event"]),
		"RAKIT_TOOL="+fmt.Sprint(payload["tool_name"]),
		"RAKIT_WORKSPACE="+r.workspace,
		"RAKIT_SESSION="+fmt.Sprint(payload["session_id"]),
	)

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	var resp hookResponse
	if out != "" && strings.HasPrefix(out, "{") {
		_ = json.Unmarshal([]byte(out), &resp)
	}
	if resp.Output == "" && out != "" && !strings.HasPrefix(out, "{") {
		resp.Output = out
	}
	if resp.Reason == "" && resp.Decision == "block" {
		resp.Reason = errOut
	}

	if runCtx.Err() == context.DeadlineExceeded {
		return resp, true, fmt.Sprintf("hook timed out after %s", timeout)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			reason := resp.Reason
			if reason == "" {
				reason = errOut
			}
			if reason == "" {
				reason = "blocked by hook"
			}
			return resp, true, reason
		}
		return resp, false, ""
	}
	if resp.Decision == "block" {
		reason := resp.Reason
		if reason == "" {
			reason = "blocked by hook"
		}
		return resp, true, reason
	}
	return resp, false, ""
}

func matchTool(pattern, tool string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	ok, err := regexp.MatchString(pattern, tool)
	return err == nil && ok
}
