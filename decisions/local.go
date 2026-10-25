package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Haslab-dev/RakitKode/llm"
)

// RiskLevel classifies the potential danger or impact of a tool call.
type RiskLevel string

const (
	RiskSafe     RiskLevel = "safe"      // Read-only, inspection, search, non-mutating
	RiskLow      RiskLevel = "low"       // Benign mutations, scratch notes, harmless queries
	RiskMedium   RiskLevel = "medium"    // File edits, builds, local test runs
	RiskHigh     RiskLevel = "high"      // Git push, global installs, process kills, system changes
	RiskCritical RiskLevel = "critical"  // Destructive operations (rm -rf, formatting, secret leakage)
)

// SafetyAssessment captures the security evaluation for a proposed tool execution.
type SafetyAssessment struct {
	ToolName           string    `json:"tool_name"`
	Risk               RiskLevel `json:"risk"`
	AutoApproveAllowed bool      `json:"auto_approve_allowed"`
	RequiresConfirm    bool      `json:"requires_confirm"`
	Reason             string    `json:"reason"`
	SuggestedHunk      string    `json:"suggested_hunk,omitempty"`
}

// LocalDecisionEngine performs high-speed local System-1 classifications for
// tool pruning, subagent routing, tool safety gating, and reflection using
// an embedded trained neural network model (trained via Python and compiled to Go).
// It runs completely offline with zero external dependencies and sub-millisecond latency
// whenever Jev API key is not configured or Jev API returns an error.
type LocalDecisionEngine struct {
	mu           sync.RWMutex
	neuralEngine *SmallNeuralEngine
	endpoint     string        // optional external endpoint if explicitly set
	model        string
	apiKey       string
	httpClient   *http.Client
	stats        LocalStats
}

// LocalStats records execution counts and latency for local decisions.
type LocalStats struct {
	TotalDecisions int            `json:"total_decisions"`
	LocalModelHits int            `json:"local_model_hits"`
	EmbeddedHits   int            `json:"embedded_hits"`
	CategoryCounts map[string]int `json:"category_counts"`
	SafetyAudits   int            `json:"safety_audits"`
	AvgLatencyMs   float64        `json:"avg_latency_ms"`
	totalLatencyMs int64
}

// NewLocalDecisionEngine creates an instance of the local smart decision engine.
func NewLocalDecisionEngine() *LocalDecisionEngine {
	return &LocalDecisionEngine{
		neuralEngine: NewSmallNeuralEngine(),
		httpClient:   &http.Client{Timeout: 1200 * time.Millisecond},
		stats: LocalStats{
			CategoryCounts: make(map[string]int),
		},
	}
}

// NeuralEngine returns the underlying trained small neural network model.
func (lde *LocalDecisionEngine) NeuralEngine() *SmallNeuralEngine {
	return lde.neuralEngine
}

// SetLocalModel configures an optional local small model inference endpoint.
func (lde *LocalDecisionEngine) SetLocalModel(endpoint, model, apiKey string) {
	lde.mu.Lock()
	defer lde.mu.Unlock()
	lde.endpoint = strings.TrimRight(endpoint, "/")
	lde.model = strings.TrimSpace(model)
	lde.apiKey = strings.TrimSpace(apiKey)
}

// GetStats returns usage and performance metrics for the local decision engine.
func (lde *LocalDecisionEngine) GetStats() LocalStats {
	lde.mu.RLock()
	defer lde.mu.RUnlock()
	copied := lde.stats
	copied.CategoryCounts = make(map[string]int, len(lde.stats.CategoryCounts))
	for k, v := range lde.stats.CategoryCounts {
		copied.CategoryCounts[k] = v
	}
	return copied
}

func (lde *LocalDecisionEngine) recordDecision(category string, usedLocalModel bool, dur time.Duration) {
	lde.mu.Lock()
	defer lde.mu.Unlock()
	lde.stats.TotalDecisions++
	if usedLocalModel {
		lde.stats.LocalModelHits++
	} else {
		lde.stats.EmbeddedHits++
	}
	if category == "" {
		category = "general"
	}
	lde.stats.CategoryCounts[category]++
	lde.stats.totalLatencyMs += dur.Milliseconds()
	if lde.stats.TotalDecisions > 0 {
		lde.stats.AvgLatencyMs = float64(lde.stats.totalLatencyMs) / float64(lde.stats.TotalDecisions)
	}
}

// Route classifies the prompt and selects the precise tool subset needed.
// It tries the configured local small model first; if not configured or unresponsive,
// it executes the embedded neural/semantic heuristic classifier.
func (lde *LocalDecisionEngine) Route(ctx context.Context, prompt string, allTools []llm.ToolDefinition) ([]llm.ToolDefinition, *RouteDecision) {
	start := time.Now()
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" || len(allTools) == 0 {
		return allTools, nil
	}

	// 1. Try fast local small model if configured
	lde.mu.RLock()
	endpoint := lde.endpoint
	model := lde.model
	apiKey := lde.apiKey
	lde.mu.RUnlock()

	if endpoint != "" && model != "" {
		dec, err := lde.querySmallModelRoute(ctx, endpoint, model, apiKey, trimmed)
		if err == nil && dec != nil {
			lde.recordDecision(dec.Category, true, time.Since(start))
			filtered := filterToolsByCategory(dec.Category, trimmed, allTools)
			return filtered, dec
		}
	}

	// 2. Explicit tool citations (e.g. computer_screenshot, call edit, tool bash)
	if mentioned := localExplicitToolMentions(trimmed, allTools); len(mentioned) > 0 {
		lde.recordDecision("explicit-mention", false, time.Since(start))
		return mentioned, &RouteDecision{
			Category:   "explicit-mention",
			Confidence: 0.99,
			NeedsTools: 1.0,
			Model:      "local-embedded",
		}
	}

	// 3. Embedded trained neural small model (trained via Python and compiled to Go)
	if lde.neuralEngine != nil {
		cat, conf, nt := lde.neuralEngine.PredictCategory(trimmed)
		if cat != "" && conf >= 0.30 {
			filtered := filterToolsByCategory(cat, trimmed, allTools)
			dec := &RouteDecision{
				Category:   cat,
				Confidence: conf,
				NeedsTools: nt,
				Model:      SmallModelVersion,
			}
			lde.recordDecision(cat, false, time.Since(start))
			return filtered, dec
		}
	}

	// 4. Fallback to embedded semantic classifier
	filtered, category, needsTools, confidence := lde.embeddedSemanticRoute(trimmed, allTools)
	dec := &RouteDecision{
		Category:   category,
		Confidence: confidence,
		NeedsTools: needsTools,
		Model:      "local-smart-classifier",
	}
	lde.recordDecision(category, false, time.Since(start))
	return filtered, dec
}

// localExplicitToolMentions identifies tools explicitly cited by name or syntax.
func localExplicitToolMentions(prompt string, allTools []llm.ToolDefinition) []llm.ToolDefinition {
	lower := strings.ToLower(prompt)
	if lower == "" {
		return nil
	}
	var out []llm.ToolDefinition
	for _, td := range allTools {
		name := strings.ToLower(strings.TrimSpace(td.Function.Name))
		if name == "" {
			continue
		}
		// Compound identifiers (contains underscore or slash or double underscore)
		if strings.Contains(name, "_") || strings.Contains(name, "/") || strings.Contains(name, "-") {
			if strings.Contains(lower, name) {
				out = append(out, td)
			}
			continue
		}
		// Bare short names require tool-citation context
		if strings.Contains(lower, "tool "+name) || strings.Contains(lower, "tool: "+name) ||
			strings.Contains(lower, "call "+name) || strings.Contains(lower, "invoke "+name) ||
			strings.Contains(lower, "`"+name+"`") || strings.Contains(lower, name+" tool") {
			out = append(out, td)
		}
	}
	return out
}

// embeddedSemanticRoute runs the multi-dimensional feature vector classifier.
func (lde *LocalDecisionEngine) embeddedSemanticRoute(prompt string, allTools []llm.ToolDefinition) ([]llm.ToolDefinition, string, float64, float64) {
	lower := strings.ToLower(prompt)

	// Screen / desktop control intent keeps all tools
	for _, kw := range []string{"screenshot", "screen capture", "tangkapan layar", "my screen", "layar", "computer_"} {
		if strings.Contains(lower, kw) {
			return filterToolsByCategory("all", prompt, allTools), "all", 1.0, 0.98
		}
	}

	type catWeight struct {
		category string
		weight   float64
		terms    []string
	}

	weights := []catWeight{
		{
			category: "git",
			weight:   2.5,
			terms: []string{
				"git ", "git status", "git diff", "git log", "git commit", "git push",
				"git pull", "git branch", "git checkout", "merge conflict", "uncommitted",
				"working tree", "stash", "rebase", "cherry-pick", "commit ",
			},
		},
		{
			category: "execute",
			weight:   2.2,
			terms: []string{
				"run ", "execute", "exec", "terminal", "bash", "shell", "build", "compile",
				"npm ", "yarn ", "pnpm ", "bun ", "cargo ", "go test", "go run", "go build",
				"make ", "pytest", "python ", "node ", "docker ", "compose", "start server",
				"restart", "jalankan", "deploy", "script",
			},
		},
		{
			category: "modify",
			weight:   2.4,
			terms: []string{
				"create file", "write", "edit", "modify", "refactor", "patch", "fix bug",
				"implement", "replace", "add function", "update the file", "delete file",
				"rename file", "ubah", "buat file", "perbaiki", "tambahkan", "ganti",
			},
		},
		{
			category: "read",
			weight:   2.0,
			terms: []string{
				"read ", "show file", "file content", "open file", "inspect file", "view file",
				"check line", "baca file", "tampilkan isi", "source code of",
			},
		},
		{
			category: "explore",
			weight:   2.2,
			terms: []string{
				"find file", "search for", "list directory", "list files", "file tree",
				"where is", "locate", "grep", "glob", "symbols", "repo structure",
				"daftar file", "cari file", "struktur folder", "scan codebase",
				"list all files", "show all files", "list files in", "files in directory",
			},
		},
		{
			category: "web",
			weight:   2.2,
			terms: []string{
				"browse", "browser", "url", "http://", "https://", "website", "scrape",
				"fetch page", "web search", "navigate to", "buka web", "situs",
			},
		},
		{
			category: "skills",
			weight:   2.0,
			terms: []string{
				"skill", "playbook", "workflow", "run skill", "load skill", "custom routine",
			},
		},
		{
			category: "mcp",
			weight:   2.5,
			terms: []string{
				"mcp", "mcp__", "mcp server", "connect mcp",
			},
		},
		{
			category: "board",
			weight:   2.8,
			terms: []string{
				"task", "tasks", "board", "kanban", "issue", "issues", "backlog", "priority",
				"todo", "buatkan task", "bikin task", "tambah task", "buat task", "hapus task",
				"update task", "status task", "tiket", "ticket",
			},
		},
	}

	scores := make(map[string]float64)
	for _, cw := range weights {
		for _, term := range cw.terms {
			if strings.Contains(lower, term) {
				scores[cw.category] += cw.weight
			}
		}
	}

	// Composite exploratory intent: list + files/directory
	if strings.Contains(lower, "list") && (strings.Contains(lower, "file") || strings.Contains(lower, "dir") || strings.Contains(lower, "folder")) {
		scores["explore"] += 3.0
	}

	// Conversational / clarification indicators
	conversationalIndicators := []string{
		"hello", "hi", "hey", "halo", "selamat", "how are you", "who are you",
		"explain concept", "what is", "why is", "tell me about", "thank", "terima kasih",
		"apa itu", "kenapa", "tolong jelaskan",
	}
	chatScore := 0.0
	for _, ci := range conversationalIndicators {
		if strings.Contains(lower, ci) {
			chatScore += 1.5
		}
	}

	bestCategory := "none"
	bestScore := 0.0
	for cat, sc := range scores {
		if sc > bestScore {
			bestScore = sc
			bestCategory = cat
		}
	}

	needsTools := 0.1
	confidence := 0.85
	if bestScore > 0 {
		needsTools = 0.95
		confidence = 0.90 + (bestScore / (bestScore + 5.0)) * 0.09
		if chatScore > bestScore*1.5 && bestScore < 3.0 {
			bestCategory = "none"
			needsTools = 0.2
			confidence = 0.88
		}
	} else if chatScore > 0 {
		bestCategory = "none"
		needsTools = 0.1
		confidence = 0.92
	}

	return filterToolsByCategory(bestCategory, prompt, allTools), bestCategory, needsTools, confidence
}

// DecideRole classifies a delegated task into the optimal subagent role.
func (lde *LocalDecisionEngine) DecideRole(ctx context.Context, task string, availableRoles []string) *RoleDecision {
	start := time.Now()
	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return &RoleDecision{
			Role:       "coding",
			Reason:     "Empty task provided; default coding agent assigned.",
			Confidence: 1.0,
			Source:     "local-default",
		}
	}

	// 1. Explicit custom role matches
	lower := strings.ToLower(trimmed)
	for _, r := range availableRoles {
		norm := strings.ToLower(strings.TrimSpace(r))
		if norm != "" && norm != "auto" && norm != "coding" && norm != "research" && norm != "planning" {
			if strings.Contains(lower, norm) {
				lde.recordDecision("role-"+norm, false, time.Since(start))
				return &RoleDecision{
					Role:       norm,
					Reason:     fmt.Sprintf("Task explicitly matches configured subagent role %q.", r),
					Confidence: 0.95,
					Source:     "local-explicit",
				}
			}
		}
	}

	// 2. Embedded trained neural small model role classification
	if lde.neuralEngine != nil {
		predRole, pConf := lde.neuralEngine.PredictRole(trimmed)
		if pConf >= 0.35 {
			mapped := "coding"
			switch predRole {
			case "researcher":
				mapped = "research"
			case "reviewer":
				mapped = "planning"
			case "coder", "debugger", "devops":
				mapped = "coding"
			}
			for _, av := range availableRoles {
				if strings.EqualFold(av, mapped) || strings.EqualFold(av, predRole) {
					lde.recordDecision("role-"+av, false, time.Since(start))
					return &RoleDecision{
						Role:       av,
						Reason:     fmt.Sprintf("Small neural model classified task as %s.", predRole),
						Confidence: pConf,
						Source:     SmallModelVersion,
					}
				}
			}
		}
	}

	// 3. Fallback semantic role scoring
	planningKeywords := []string{
		"plan", "roadmap", "architecture", "design", "specification", "break down",
		"breakdown", "decompose", "strategy", "phases", "milestone", "blueprint",
		"step-by-step", "rfc", "requirements", "rencana", "arsitektur",
	}
	researchKeywords := []string{
		"research", "investigate", "find where", "where is", "trace", "explore",
		"audit", "analyze", "inspect", "read", "understand", "explain code", "locate",
		"how does", "why does", "search for", "look into", "check how", "review codebase",
		"find symbol", "tinjau", "selidiki", "cari referensi", "audit keamanan",
	}
	codingKeywords := []string{
		"implement", "fix", "bug", "refactor", "code", "write", "edit", "patch",
		"create", "add", "delete", "remove", "build", "test", "run", "script",
		"compile", "lint", "buat", "ubah", "perbaiki", "kode",
	}

	var planScore, resScore, codeScore float64
	for _, w := range planningKeywords {
		if strings.Contains(lower, w) {
			planScore += 2.0
		}
	}
	for _, w := range researchKeywords {
		if strings.Contains(lower, w) {
			resScore += 2.0
		}
	}
	for _, w := range codingKeywords {
		if strings.Contains(lower, w) {
			codeScore += 2.0
		}
	}

	var chosenRole string
	var reason string
	var conf float64

	switch {
	case planScore > 0 && planScore >= resScore && planScore >= codeScore:
		chosenRole = "planning"
		reason = "Local decision engine identified architectural synthesis, task decomposition, and milestone planning."
		conf = 0.92
	case resScore > 0 && resScore >= codeScore:
		chosenRole = "research"
		reason = "Local decision engine identified codebase exploration, deep symbol search, and audit analysis."
		conf = 0.94
	case codeScore > 0:
		chosenRole = "coding"
		reason = "Local decision engine identified hands-on code modification, debugging, and execution."
		conf = 0.95
	default:
		chosenRole = "coding"
		reason = "Standard implementation role with complete tool and verification permissions."
		conf = 0.85
	}

	lde.recordDecision("role-"+chosenRole, false, time.Since(start))
	return &RoleDecision{
		Role:       chosenRole,
		Reason:     reason,
		Confidence: conf,
		Source:     "local-smart-classifier",
	}
}

// DecideSubagent matches a delegated task against candidates using semantic scoring.
func (lde *LocalDecisionEngine) DecideSubagent(ctx context.Context, task string, candidates []SubagentCandidate) *SubagentDecision {
	start := time.Now()
	trimmed := strings.TrimSpace(task)
	if len(candidates) == 0 {
		return &SubagentDecision{
			SelectedID: "coding",
			Name:       "Coding Agent",
			Role:       "coding",
			Reason:     "No subagents configured; fallback to coding agent.",
			Confidence: 0.8,
			Source:     "local-default",
		}
	}

	lowerTask := strings.ToLower(trimmed)
	bestScore := -1.0
	var bestCandidate *SubagentCandidate
	var matchReason string

	for i := range candidates {
		c := &candidates[i]
		score := 0.0
		lowerName := strings.ToLower(c.Name)
		lowerDesc := strings.ToLower(c.Description)

		// Name matches
		for _, w := range strings.Fields(lowerName) {
			if len(w) > 3 && strings.Contains(lowerTask, w) {
				score += 4.0
			}
		}

		// Description matches
		for _, w := range strings.Fields(lowerDesc) {
			w = strings.Trim(w, ",.;:()\"'")
			if len(w) > 3 && strings.Contains(lowerTask, w) {
				score += 2.0
			}
		}

		// Role alignment
		if strings.Contains(lowerTask, c.Role) {
			score += 3.0
		}

		if score > bestScore && score > 2.0 {
			bestScore = score
			bestCandidate = c
			matchReason = fmt.Sprintf("Task requirements align strongly with %q specialization (%s).", c.Name, c.Role)
		}
	}

	if bestCandidate != nil {
		lde.recordDecision("subagent-"+bestCandidate.ID, false, time.Since(start))
		return &SubagentDecision{
			SelectedID: bestCandidate.ID,
			Name:       bestCandidate.Name,
			Role:       bestCandidate.Role,
			Reason:     matchReason,
			Confidence: 0.94,
			Source:     "local-smart-classifier",
		}
	}

	// Fallback to role-based selection
	roleDec := lde.DecideRole(ctx, task, nil)
	for _, c := range candidates {
		if strings.EqualFold(c.Role, roleDec.Role) || strings.EqualFold(c.ID, roleDec.Role) {
			lde.recordDecision("subagent-"+c.ID, false, time.Since(start))
			return &SubagentDecision{
				SelectedID: c.ID,
				Name:       c.Name,
				Role:       c.Role,
				Reason:     roleDec.Reason,
				Confidence: roleDec.Confidence,
				Source:     "local-role-fallback",
			}
		}
	}

	first := candidates[0]
	lde.recordDecision("subagent-"+first.ID, false, time.Since(start))
	return &SubagentDecision{
		SelectedID: first.ID,
		Name:       first.Name,
		Role:       first.Role,
		Reason:     roleDec.Reason,
		Confidence: 0.85,
		Source:     "local-first-candidate",
	}
}

// AssessToolSafety performs a security and risk audit of a tool invocation.
// It categorizes the operation into safe, low, medium, high, or critical risk.
func (lde *LocalDecisionEngine) AssessToolSafety(toolName string, args map[string]any) SafetyAssessment {
	lde.mu.Lock()
	lde.stats.SafetyAudits++
	lde.mu.Unlock()

	normName := strings.ToLower(strings.TrimSpace(toolName))

	// 1. Read-only tools are always safe
	readOnlyTools := map[string]bool{
		"read": true, "read_multiple": true, "read_directory_files": true,
		"search": true, "find": true, "glob": true, "index": true,
		"git_status": true, "task_output": true, "task_list": true,
		"web_fetch": true, "web_search": true, "todo": true, "ask": true,
		"list_skills": true, "load_skill": true, "skill": true,
	}
	if readOnlyTools[normName] {
		return SafetyAssessment{
			ToolName:           toolName,
			Risk:               RiskSafe,
			AutoApproveAllowed: true,
			RequiresConfirm:    false,
			Reason:             "Read-only non-destructive operation.",
		}
	}

	// 2. File editing tools (medium risk)
	if normName == "edit" || normName == "multiedit" || normName == "write" {
		path, _ := args["path"].(string)
		if path == "" {
			path, _ = args["target_file"].(string)
		}
		// Guard sensitive files
		if isSensitivePath(path) {
			return SafetyAssessment{
				ToolName:           toolName,
				Risk:               RiskHigh,
				AutoApproveAllowed: false,
				RequiresConfirm:    true,
				Reason:             fmt.Sprintf("Modification of sensitive path %q requires explicit user approval.", path),
			}
		}
		return SafetyAssessment{
			ToolName:           toolName,
			Risk:               RiskMedium,
			AutoApproveAllowed: true,
			RequiresConfirm:    false,
			Reason:             fmt.Sprintf("File modification within workspace (%s).", path),
		}
	}

	// 3. Bash and shell tools
	if normName == "bash" || normName == "background_shell" || normName == "run_code" || normName == "eval" {
		cmd, _ := args["command"].(string)
		if cmd == "" {
			cmd, _ = args["cmd"].(string)
		}
		lowerCmd := strings.ToLower(cmd)

		// Critical risk: destructive system commands
		criticalPatterns := []string{
			"rm -rf /", "rm -rf ~", "mkfs", "dd if=", ":(){ :|:& };:",
			"> /dev/sda", "chmod -r 777 /", "shutdown", "reboot",
		}
		for _, cp := range criticalPatterns {
			if strings.Contains(lowerCmd, cp) {
				return SafetyAssessment{
					ToolName:           toolName,
					Risk:               RiskCritical,
					AutoApproveAllowed: false,
					RequiresConfirm:    true,
					Reason:             fmt.Sprintf("Potentially catastrophic destructive shell command detected: %q", cmd),
				}
			}
		}

		// High risk: git push/force, package publication, secret printing
		highPatterns := []string{
			"git push", "git reset --hard", "npm publish", "cargo publish",
			"cat ~/.ssh", "cat ~/.aws", "env ", "printenv", "curl -x post",
		}
		for _, hp := range highPatterns {
			if strings.Contains(lowerCmd, hp) {
				return SafetyAssessment{
					ToolName:           toolName,
					Risk:               RiskHigh,
					AutoApproveAllowed: false,
					RequiresConfirm:    true,
					Reason:             fmt.Sprintf("Potentially impactful shell operation detected: %q", cmd),
				}
			}
		}

		// Safe / harmless bash queries
		harmlessCommands := []string{"ls", "pwd", "date", "whoami", "echo", "cat", "git diff", "git log"}
		firstWord := strings.Fields(lowerCmd)
		if len(firstWord) > 0 {
			for _, hc := range harmlessCommands {
				if firstWord[0] == hc {
					return SafetyAssessment{
						ToolName:           toolName,
						Risk:               RiskLow,
						AutoApproveAllowed: true,
						RequiresConfirm:    false,
						Reason:             "Safe informational terminal query.",
					}
				}
			}
		}

		return SafetyAssessment{
			ToolName:           toolName,
			Risk:               RiskMedium,
			AutoApproveAllowed: true,
			RequiresConfirm:    false,
			Reason:             "Standard terminal command execution.",
		}
	}

	// Default fallback for unknown or MCP tools
	return SafetyAssessment{
		ToolName:           toolName,
		Risk:               RiskMedium,
		AutoApproveAllowed: true,
		RequiresConfirm:    false,
		Reason:             "Standard tool execution.",
	}
}

// SuggestReflection provides actionable recovery advice when a tool execution fails.
func (lde *LocalDecisionEngine) SuggestReflection(toolName string, args map[string]any, output string) string {
	lowerOut := strings.ToLower(output)

	switch toolName {
	case "edit", "multiedit":
		if strings.Contains(lowerOut, "target text not found") || strings.Contains(lowerOut, "mismatch") || strings.Contains(lowerOut, "not found") {
			return "Reflection: The target lines in the edit tool did not match the file contents. Re-read the file lines around that area to verify exact whitespace, indentation, and surrounding lines before retrying."
		}
		if strings.Contains(lowerOut, "permission denied") || strings.Contains(lowerOut, "read-only") {
			return "Reflection: Write permission was denied for the target file. Check file attributes or verify path."
		}

	case "bash", "background_shell":
		if strings.Contains(lowerOut, "command not found") {
			return "Reflection: The command binary is missing or not installed in PATH. Check installation or use an alternative standard tool."
		}
		if strings.Contains(lowerOut, "no such file or directory") {
			return "Reflection: A referenced path or directory does not exist. List directory contents first to confirm the exact filename and structure."
		}
		if strings.Contains(lowerOut, "exit status 1") || strings.Contains(lowerOut, "failed") {
			return "Reflection: The process exited with an error. Review the error message above carefully to pinpoint the root cause before retrying."
		}

	case "read":
		if strings.Contains(lowerOut, "no such file") || strings.Contains(lowerOut, "not found") {
			return "Reflection: The requested file path was not found. Use 'find' or 'glob' to locate the exact filename."
		}
	}

	return ""
}

func isSensitivePath(p string) bool {
	lower := strings.ToLower(p)
	sensitive := []string{
		".env", "id_rsa", "id_ed25519", "credentials", ".git/config",
		"secrets", "token", ".npmrc", ".pypirc",
	}
	for _, s := range sensitive {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// querySmallModelRoute sends a concise structured schema request to a local small model endpoint.
func (lde *LocalDecisionEngine) querySmallModelRoute(ctx context.Context, endpoint, model, apiKey, prompt string) (*RouteDecision, error) {
	url := endpoint
	if !strings.HasSuffix(url, "/chat/completions") {
		url += "/chat/completions"
	}

	systemInstruction := `You are a high-speed System-1 tool router. Analyze the user prompt and respond with pure JSON only:
{"category": "explore|read|modify|execute|git|web|skills|mcp|none|all", "needs_tools": 0.0-1.0, "confidence": 0.0-1.0}`

	type chatReq struct {
		Model       string            `json:"model"`
		Messages    []map[string]string `json:"messages"`
		Temperature float64           `json:"temperature"`
		MaxTokens   int               `json:"max_tokens"`
	}

	body := chatReq{
		Model: model,
		Messages: []map[string]string{
			{"role": "system", "content": systemInstruction},
			{"role": "user", "content": prompt},
		},
		Temperature: 0.0,
		MaxTokens:   80,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := lde.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local model status %d", resp.StatusCode)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("failed to decode local model response: %w", err)
	}

	raw := parsed.Choices[0].Message.Content
	// Extract JSON
	re := regexp.MustCompile(`\{[^{}]*\}`)
	matched := re.FindString(raw)
	if matched == "" {
		return nil, fmt.Errorf("no json in small model reply")
	}

	var out struct {
		Category   string  `json:"category"`
		NeedsTools float64 `json:"needs_tools"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(matched), &out); err != nil {
		return nil, err
	}

	cat := strings.ToLower(strings.TrimSpace(out.Category))
	if cat == "" {
		cat = "general"
	}

	return &RouteDecision{
		Category:   cat,
		NeedsTools: out.NeedsTools,
		Confidence: out.Confidence,
		Model:      "local-small-model:" + model,
	}, nil
}
