package agent

import (
	"fmt"
	"strings"
)

// Session phases (rule-inferred, no model call).
const (
	PhaseDiscovery      = "discovery"
	PhasePlanning       = "planning"
	PhaseImplementation = "implementation"
	PhaseDebugging      = "debugging"
	PhaseVerification   = "verification"
	PhaseWrapup         = "wrapup"
)

var debugKeywords = []string{
	"error", "fail", "bug", "broken", "crash", "panic", "doesn't work", "does not work",
	"not working", "exception", "stack trace", "regression",
}

var discoveryTools = map[string]bool{
	"read": true, "read_file": true, "read_multiple": true, "read_directory_files": true,
	"search": true, "search_workspace": true, "find": true, "glob": true, "grep": true,
	"rg": true, "index": true, "symbol_search": true, "list_dir": true, "ls": true,
	"git_status": true, "web_fetch": true, "web_search": true, "view_file": true,
	"cat": true, "tool_search": true, "recall_memory": true, "search_brain": true,
	"read_digest": true, "load_skill": true, "skill": true,
}

var planningTools = map[string]bool{
	"update_tasks": true, "todo": true, "enter_plan_mode": true, "exit_plan_mode": true,
}

var verificationTools = map[string]bool{
	"bash": true, "run_shell": true, "exec": true, "run_command": true,
	"eval": true, "run_code": true, "python_eval": true, "python": true,
	"git_status": true, "diff": true, "task_output": true,
}

// inferSessionPhase classifies the session's current working phase from cheap
// deterministic signals only: the tools executed since the last user message,
// the task checklist state, and keywords in the latest user prompt.
//
// Priority (most specific first):
//  1. wrapup        — every task completed (or goal met) and the turn produced prose
//  2. debugging     — user reports an error/failure in the latest prompt
//  3. verification  — the turn ran shells/tests after (or instead of) mutations
//  4. implementation— the turn mutated files
//  5. planning      — the turn was mostly checklist/plan management
//  6. discovery     — default: reading and searching
func inferSessionPhase(sess *Session) string {
	if sess == nil {
		return ""
	}

	// Collect the tools of the current (latest) turn.
	start := len(sess.Messages)
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			start = i
			break
		}
	}
	var turnTools []string
	userText := ""
	for i, msg := range sess.Messages[start:] {
		if i == 0 && msg.Role == "user" {
			userText = strings.ToLower(msg.Text())
		}
		for _, b := range msg.Content {
			if b.Type == "tool_use" && b.Name != "" {
				turnTools = append(turnTools, b.Name)
			}
			if b.Type == "tool_result" && b.Name != "" {
				turnTools = append(turnTools, b.Name)
			}
		}
	}

	// 1. Wrapup: a task list exists, everything is completed.
	if len(sess.Tasks) > 0 {
		allDone := true
		anyInProgress := false
		for _, t := range sess.Tasks {
			switch {
			case t.Status == TaskInProgress:
				anyInProgress = true
			case t.Status != TaskCompleted && !t.Completed:
				allDone = false
			}
		}
		if allDone && !anyInProgress {
			return PhaseWrapup
		}
	}

	// 2. Debugging: the latest user prompt reports breakage.
	if userText != "" {
		for _, kw := range debugKeywords {
			if strings.Contains(userText, kw) {
				return PhaseDebugging
			}
		}
	}

	// 3-6: classify by the tool mix of the current turn.
	mutating, verifying, planning, discovering := 0, 0, 0, 0
	for _, name := range turnTools {
		switch {
		case verificationTools[name]:
			verifying++
		case planningTools[name]:
			planning++
		case discoveryTools[name]:
			discovering++
		default:
			mutating++
		}
	}
	switch {
	case mutating > 0 && verifying > 0 && mutating <= verifying:
		return PhaseVerification
	case mutating > 0:
		return PhaseImplementation
	case verifying > 0:
		return PhaseVerification
	case planning > 0:
		return PhasePlanning
	default:
		return PhaseDiscovery
	}
}

// buildSessionPhaseContext renders the stable per-turn `phase:` context line
// injected next to the orchestration context so both the model and any lead
// agent can see where the session is in its lifecycle. Empty for sessions
// with nothing to say (no messages yet).
func buildSessionPhaseContext(sess *Session) string {
	phase := inferSessionPhase(sess)
	if phase == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<session_phase>\n")
	fmt.Fprintf(&sb, "phase: %s\n", phase)
	switch phase {
	case PhaseDiscovery:
		sb.WriteString("Explore before editing; gather the exact files and facts needed for the goal.\n")
	case PhasePlanning:
		sb.WriteString("Decompose the goal with update_tasks before mutating anything.\n")
	case PhaseImplementation:
		sb.WriteString("Apply the smallest correct change; keep the task checklist updated.\n")
	case PhaseDebugging:
		sb.WriteString("Reproduce first, then fix the cause — do not patch symptoms.\n")
	case PhaseVerification:
		sb.WriteString("Run the relevant build/tests and report objective pass/fail results.\n")
	case PhaseWrapup:
		sb.WriteString("Work is verified and complete; summarize outcomes and remaining next steps.\n")
	}
	sb.WriteString("</session_phase>")
	return sb.String()
}
