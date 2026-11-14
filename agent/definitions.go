package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Haslab-dev/RakitKode/events"
	"github.com/google/uuid"
)

// AgentDefinition is a pre-configured agent persona the user defines once (in global
// settings) and selects in chat or spawns via multi-agent orchestration.
type AgentDefinition struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Role        RoleFilter `json:"role_filter,omitempty"`
	Model       string     `json:"model,omitempty"`
	Prompt      string     `json:"prompt,omitempty"`
	Rules       string     `json:"rules,omitempty"`
	Color       string     `json:"color,omitempty"`
	Builtin     bool       `json:"builtin,omitempty"`
	AllowBash   bool       `json:"allow_bash"`
	ReadOnly    bool       `json:"read_only"`
	Tools       []string   `json:"tools,omitempty"`       // tool categories or tool names (e.g. "filesystem", "terminal", "git", "web", "browser")
	MCPServers  []string   `json:"mcp_servers,omitempty"` // enabled MCP server names (e.g. "exa", "figma")
	Skills      []string   `json:"skills,omitempty"`      // associated skill IDs
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// OrchestrationConfig configures multi-agent coordination (Claude Code / Codex
// style Orchestrator lead agent, sub-agent concurrency, depth limits, and model tiering).
type OrchestrationConfig struct {
	Enabled             bool       `json:"enabled"`
	DefaultRole         RoleFilter `json:"default_role"`
	MaxConcurrent       int        `json:"max_concurrent"`
	MaxDepth            int        `json:"max_depth"`
	AutoRoute           bool       `json:"auto_route"`
	DefaultSubagentTier string     `json:"default_subagent_tier"` // "auto", "weak", "strong", "inherit"
	ParallelExploration bool       `json:"parallel_exploration"`
}

// DefaultOrchestrationConfig returns the default multi-agent harness configuration
// with the Orchestrator acting as the primary lead agent.
func DefaultOrchestrationConfig() OrchestrationConfig {
	return OrchestrationConfig{
		Enabled:             true,
		DefaultRole:         RoleOrchestrator,
		MaxConcurrent:       4,
		MaxDepth:            3,
		AutoRoute:           true,
		DefaultSubagentTier: "auto",
		ParallelExploration: true,
	}
}

func (m *Manager) orchestrationPath() string {
	if m.storePath != "" {
		return filepath.Join(filepath.Dir(m.storePath), "forgeade.orchestration.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".forge-ade", "forgeade.orchestration.json")
}

func normalizeOrchestrationConfig(cfg OrchestrationConfig) OrchestrationConfig {
	if cfg.DefaultRole == "" {
		if cfg.Enabled {
			cfg.DefaultRole = RoleOrchestrator
		} else {
			cfg.DefaultRole = RoleCoding
		}
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 4
	} else if cfg.MaxConcurrent > 16 {
		cfg.MaxConcurrent = 16
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 3
	} else if cfg.MaxDepth > 8 {
		cfg.MaxDepth = 8
	}
	switch strings.ToLower(strings.TrimSpace(cfg.DefaultSubagentTier)) {
	case "weak", "strong", "inherit", "auto":
		cfg.DefaultSubagentTier = strings.ToLower(strings.TrimSpace(cfg.DefaultSubagentTier))
	default:
		cfg.DefaultSubagentTier = "auto"
	}
	return cfg
}

// GetOrchestrationConfig returns the active multi-agent orchestration configuration.
func (m *Manager) GetOrchestrationConfig() OrchestrationConfig {
	m.mu.RLock()
	cfg := m.orchConfig
	m.mu.RUnlock()
	if cfg.DefaultRole == "" {
		return DefaultOrchestrationConfig()
	}
	return cfg
}

// SaveOrchestrationConfig persists orchestration settings and dynamically updates
// sub-agent pool concurrency and recursion limits without restarting sessions.
func (m *Manager) SaveOrchestrationConfig(cfg OrchestrationConfig) error {
	cfg = normalizeOrchestrationConfig(cfg)

	m.mu.Lock()
	m.orchConfig = cfg
	m.maxSubagentDepth = cfg.MaxDepth
	path := m.orchestrationPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if data, err := json.MarshalIndent(cfg, "", "  "); err != nil {
		m.mu.Unlock()
		return err
	} else if err := os.WriteFile(path, data, 0644); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	m.semMu.Lock()
	m.semMax = cfg.MaxConcurrent
	if m.semNotify != nil {
		close(m.semNotify)
		m.semNotify = make(chan struct{})
	}
	m.semMu.Unlock()

	m.ConfigChanged()
	return nil
}

func (m *Manager) loadOrchestrationConfig() {
	cfg := DefaultOrchestrationConfig()
	loadedFromDisk := false
	path := m.orchestrationPath()
	if data, err := os.ReadFile(path); err == nil {
		var loaded OrchestrationConfig
		if err := json.Unmarshal(data, &loaded); err == nil {
			cfg = normalizeOrchestrationConfig(loaded)
			loadedFromDisk = true
		}
	}

	m.mu.Lock()
	if !loadedFromDisk {
		if m.maxSubagentDepth > 0 {
			cfg.MaxDepth = m.maxSubagentDepth
		}
		m.semMu.Lock()
		if m.semMax > 0 {
			cfg.MaxConcurrent = m.semMax
		}
		m.semMu.Unlock()
	}
	m.orchConfig = cfg
	if loadedFromDisk && cfg.MaxDepth > 0 {
		m.maxSubagentDepth = cfg.MaxDepth
	}
	m.mu.Unlock()

	if loadedFromDisk && cfg.MaxConcurrent > 0 {
		m.semMu.Lock()
		m.semMax = cfg.MaxConcurrent
		m.semMu.Unlock()
	}
}

// buildOrchestrationContext renders the active <orchestration_protocol> and
// <available_subagents> persona catalog for lead/root sessions when multi-agent
// orchestration is enabled.
func (m *Manager) buildOrchestrationContext(sess *Session) string {
	if sess == nil {
		return ""
	}
	cfg := m.GetOrchestrationConfig()
	if !cfg.Enabled {
		return ""
	}
	// Inject the full orchestrator catalog only on root coordinator sessions
	// or explicit orchestrator personas to keep leaf sub-agents focused and cheap.
	if sess.ParentSessionID != "" && sess.RoleFilter != RoleOrchestrator {
		return ""
	}

	defs := m.ListAgentDefinitions()
	var sb strings.Builder
	sb.WriteString("<orchestration_protocol>\n")
	sb.WriteString(fmt.Sprintf("Multi-agent orchestration is ACTIVE (max_concurrent=%d, max_depth=%d, auto_route=%v, parallel_exploration=%v).\n",
		cfg.MaxConcurrent, cfg.MaxDepth, cfg.AutoRoute, cfg.ParallelExploration))
	sb.WriteString("Operate like Claude Code / Codex lead coordination:\n")
	sb.WriteString("- Answer simple questions or trivial 1-file edits directly without delegation overhead.\n")
	sb.WriteString("- For broad exploration, complex multi-file engineering, architecture design, or independent parallel subtasks: spawn specialized sub-agent personas via `task` (blocking) or `invoke_subagent` (background/parallel).\n")
	sb.WriteString("- Pass explicit file paths, scope, and constraints in `context` or `custom_instructions` so sub-agents succeed in one shot.\n")
	sb.WriteString("- Never spawn duplicate sub-agents for an already running identical subtask.\n")
	sb.WriteString("</orchestration_protocol>\n\n<available_subagents>\n")
	for _, d := range defs {
		if d.Role == RoleOrchestrator || d.ID == "orchestrator" {
			continue
		}
		access := "read-write"
		if d.ReadOnly {
			access = "read-only"
		}
		bashMode := "no-bash"
		if d.AllowBash {
			bashMode = "bash"
		}
		modelNote := ""
		if d.Model != "" {
			modelNote = ", model=" + d.Model
		}
		sb.WriteString(fmt.Sprintf("- %s (%s, role=%s, %s, %s%s): %s\n",
			d.ID, d.Name, d.Role, access, bashMode, modelNote, d.Description))
	}
	sb.WriteString("</available_subagents>")
	return sb.String()
}

// definitionsPath returns where ForgeADE stores pre-configured agent
// definitions (~/.forge-ade/forgeade.agents.json).
func (m *Manager) definitionsPath() string {
	if m.storePath != "" {
		return filepath.Join(filepath.Dir(m.storePath), "forgeade.agents.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".forge-ade", "forgeade.agents.json")
}

// ListAgentDefinitions returns all pre-configured agent personas in a
// deterministic order: canonical built-in archetypes (with any user overrides
// applied in-place) followed by sorted user-created custom personas.
func (m *Manager) ListAgentDefinitions() []AgentDefinition {
	m.mu.RLock()
	userDefs := make(map[string]AgentDefinition, len(m.definitions))
	for id, d := range m.definitions {
		userDefs[id] = d
	}
	m.mu.RUnlock()

	builtins := builtinAgentDefs()
	builtinIDs := make(map[string]bool, len(builtins))
	defs := make([]AgentDefinition, 0, len(builtins)+len(userDefs))
	for _, b := range builtins {
		builtinIDs[b.ID] = true
		if override, ok := userDefs[b.ID]; ok {
			override.Builtin = true
			if override.Color == "" {
				override.Color = b.Color
			}
			defs = append(defs, override)
		} else {
			defs = append(defs, b)
		}
	}

	var custom []AgentDefinition
	for id, d := range userDefs {
		if !builtinIDs[id] {
			d.Builtin = false
			custom = append(custom, d)
		}
	}
	sort.Slice(custom, func(i, j int) bool {
		ni := strings.ToLower(custom[i].Name)
		nj := strings.ToLower(custom[j].Name)
		if ni != nj {
			return ni < nj
		}
		return custom[i].ID < custom[j].ID
	})
	defs = append(defs, custom...)
	return defs
}

// builtinAgentDefs are the always-available Claude Code / Codex style personas.
func builtinAgentDefs() []AgentDefinition {
	return []AgentDefinition{
		{
			ID:          "orchestrator",
			Name:        "Orchestrator Agent",
			Description: "Lead coordinator that decomposes multi-step goals and dispatches specialized personas to deliver verified solutions.",
			Role:        RoleOrchestrator,
			Color:       "accent",
			Builtin:     true,
			Prompt: "You are the Master Orchestrator Agent, operating like Claude Code / OpenAI Codex lead coordination. " +
				"Analyze the user's objective, break non-trivial work into phases with update_tasks, and delegate focused subtasks in parallel to specialized personas (research, planning, coding, reviewer, testing) using task or invoke_subagent so their exploration never bloats your master context. " +
				"Handle simple questions or tiny single-file edits directly when delegation is unnecessary. Synthesize all findings and verify the final result before reporting back.",
			Rules:     "Never duplicate work across sub-agents; pass clear file targets and constraints when delegating; verify modified code with tests or reviewer checks before concluding.",
			AllowBash: true,
			ReadOnly:  false,
			Tools:     []string{"filesystem", "terminal", "git", "web", "browser", "board"},
			Skills:    []string{"grill-me", "to-spec", "implement"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "planning",
			Name:        "Planning Agent",
			Description: "Decompose complex requirements, map dependencies, identify edge cases, and design step-by-step blueprints.",
			Role:        RolePlanning,
			Color:       "info",
			Builtin:     true,
			Prompt: "You are a Principal Software Architect and Planning Persona. " +
				"Inspect relevant files, interfaces, and callers strictly in read-only mode, identify constraints and failure modes, and produce a concrete, actionable implementation plan with exact file paths, symbol names, and verification steps.",
			Rules:     "Do not mutate workspace files; back every architectural claim with observed files and symbols.",
			AllowBash: false,
			ReadOnly:  true,
			Tools:     []string{"filesystem", "git"},
			Skills:    []string{"wayfinder", "to-tickets", "triage", "grill-me", "to-spec"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "research",
			Name:        "Research Agent",
			Description: "Explore the codebase, trace symbols and dependencies, synthesize insights, and research documentation.",
			Role:        RoleResearch,
			Color:       "info",
			Builtin:     true,
			Prompt: "You are a high-signal Research & Exploration Persona. " +
				"Trace symbol definitions, callers, configuration paths, and documentation rapidly inside your isolated context window. Return a concise, factual summary citing exact file paths, line numbers, and key findings.",
			Rules:     "Read-only exploration; verify symbols directly in source files instead of guessing.",
			AllowBash: true,
			ReadOnly:  true,
			Tools:     []string{"filesystem", "terminal", "web"},
			Skills:    []string{"grill-me", "to-spec", "prototype", "source-verification", "academic-literature-review", "citation-tracking", "evidence-synthesis"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "coding",
			Name:        "Coding Agent",
			Description: "Write clean, production-ready code, implement features, fix bugs, run builds, and verify changes.",
			Role:        RoleCoding,
			Color:       "success",
			Builtin:     true,
			Prompt: "You are a Senior Software Engineer Persona focused on surgical, idiomatic implementation. " +
				"Inspect existing patterns first, apply minimal clean diffs with edit/multiedit, avoid duplicating logic, and run relevant build/test checks to verify every change.",
			Rules:     "Preserve unrelated comments and behavior; run tests or vet commands after edits before reporting completion.",
			AllowBash: true,
			ReadOnly:  false,
			Tools:     []string{"filesystem", "terminal", "git", "web", "browser"},
			Skills:    []string{"implement", "grill-with-docs", "improve-codebase-architecture", "to-spec", "prototype"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "reviewer",
			Name:        "Review Agent",
			Description: "Inspect git diffs and pull requests for correctness, maintainability, and behavioral regressions.",
			Role:        RoleReviewer,
			Color:       "warning",
			Builtin:     true,
			Prompt: "You are a Staff Code Reviewer & Quality Auditor Persona. " +
				"Critically review diffs, control flows, concurrency/locking, error handling, edge cases, and adherence to project conventions. Report concrete findings ordered by severity with exact file and line citations.",
			Rules:     "Do not modify code directly unless explicitly instructed; remain objective and evidence-driven.",
			AllowBash: true,
			ReadOnly:  true,
			Tools:     []string{"filesystem", "terminal", "git"},
			Skills:    []string{"grill-me", "improve-codebase-architecture"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "testing",
			Name:        "Verification & QA Agent",
			Description: "Validate correctness, reproduce regressions, benchmark coverage, and confirm edge-case behavior.",
			Role:        RoleTesting,
			Color:       "success",
			Builtin:     true,
			Prompt: "You are a Verification & QA Engineering Persona. " +
				"Design and execute targeted unit/integration/race tests to reproduce defects or verify new functionality, and report exact pass/fail outputs and remaining gaps.",
			Rules:     "Never relax assertions or skip failing checks to make tests pass.",
			AllowBash: true,
			ReadOnly:  false,
			Tools:     []string{"filesystem", "terminal", "git"},
			Skills:    []string{"implement"},
			CreatedAt: time.Unix(0, 0),
			UpdatedAt: time.Unix(0, 0),
		},
		{
			ID:          "custom",
			Name:        "Custom Agent",
			Description: "General assistant for ad-hoc tasks.",
			Role:        RoleCustom,
			Builtin:     true,
			AllowBash:   true,
			ReadOnly:    false,
			Tools:       []string{"filesystem", "terminal", "git", "web", "browser"},
			CreatedAt:   time.Unix(0, 0),
			UpdatedAt:   time.Unix(0, 0),
		},
	}
}

func isBuiltinAgentID(id string) bool {
	for _, b := range builtinAgentDefs() {
		if b.ID == id {
			return true
		}
	}
	return false
}

// SaveAgentDefinition creates or updates a pre-configured agent definition.
func (m *Manager) SaveAgentDefinition(def AgentDefinition) (AgentDefinition, error) {
	m.mu.Lock()
	if strings.TrimSpace(def.Name) == "" {
		m.mu.Unlock()
		return def, fmt.Errorf("agent name is required")
	}
	now := time.Now()
	if def.ID == "" {
		def.ID = uuid.New().String()
		def.CreatedAt = now
	}
	def.Builtin = isBuiltinAgentID(def.ID)
	def.UpdatedAt = now
	if def.Role == "" {
		def.Role = RoleCoding
	}
	m.definitions[def.ID] = def
	m.saveDefinitionsLocked()
	m.mu.Unlock()

	m.ConfigChanged()
	return def, nil
}

// DeleteAgentDefinition removes a custom agent definition (or clears a built-in override).
func (m *Manager) DeleteAgentDefinition(id string) error {
	m.mu.Lock()
	delete(m.definitions, id)
	m.saveDefinitionsLocked()
	m.mu.Unlock()

	m.ConfigChanged()
	return nil
}

// ResetBuiltinDefinition removes any user customization for a built-in persona ID
// so it reverts to its factory definition.
func (m *Manager) ResetBuiltinDefinition(id string) error {
	if !isBuiltinAgentID(id) {
		return fmt.Errorf("%q is not a built-in persona", id)
	}
	return m.DeleteAgentDefinition(id)
}

// CreateSessionFromDefinition creates a chat session from a pre-configured
// agent definition, scoped to the given project folder.
func (m *Manager) CreateSessionFromDefinition(defID string, folder string) (*Session, error) {
	def, ok := m.getDefinition(defID)
	if !ok {
		return nil, fmt.Errorf("agent definition %s not found", defID)
	}

	projFolder := m.normalizeFolder(folder)
	sysPrompt := buildSystemPrompt(def.Role, def.Prompt, def.Rules)

	m.mu.Lock()
	now := time.Now()
	sess := &Session{
		ID:           uuid.New().String(),
		Name:         def.Name,
		RoleFilter:   def.Role,
		Model:        def.Model,
		AllowBash:    def.AllowBash,
		ReadOnly:     def.ReadOnly,
		Tools:        def.Tools,
		MCPServers:   def.MCPServers,
		Skills:       def.Skills,
		State:        StateIdle,
		Folder:       projFolder,
		ProjectName:  filepath.Base(projFolder),
		Messages:     make([]AgentMessage, 0),
		Tasks:        make([]TaskItem, 0),
		SystemPrompt: sysPrompt,
		CustomPrompt: def.Prompt,
		CustomRules:  def.Rules,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.sessions[sess.ID] = sess
	m.saveSessionsLocked()
	m.mu.Unlock()

	// Apply the definition's model if set.
	if def.Model != "" && m.llmClient != nil {
		m.llmClient.SetActiveModelByID(def.Model)
	}

	// Emit so the frontend (ShellScreen, sidebar) refreshes the session list.
	m.emitSessionUpdate(sess.ID)

	return sess, nil
}

// ApplyDefinitionToSession re-configures an EXISTING session to use a
// pre-configured agent definition — changing its context (role, system prompt,
// custom rules, model) without creating a new session. Re-selecting an agent
// mode in the UI should switch the current session's behavior, not spawn a
// new tab.
func (m *Manager) ApplyDefinitionToSession(sessionID string, defID string) error {
	def, ok := m.getDefinition(defID)
	if !ok {
		return fmt.Errorf("agent definition %s not found", defID)
	}

	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session %s not found", sessionID)
	}
	// Changing agent mode re-configures the session's CONTEXT only — the
	// session title must not be overwritten by the definition's name.
	sess.RoleFilter = def.Role
	sess.CustomPrompt = def.Prompt
	sess.CustomRules = def.Rules
	sess.SystemPrompt = buildSystemPrompt(def.Role, def.Prompt, def.Rules)
	if def.Model != "" {
		sess.Model = def.Model
	}
	sess.AllowBash = def.AllowBash
	sess.ReadOnly = def.ReadOnly
	sess.Tools = def.Tools
	sess.MCPServers = def.MCPServers
	sess.Skills = def.Skills
	sess.UpdatedAt = time.Now()
	m.saveSessionsLocked()
	m.mu.Unlock()

	if def.Model != "" && m.llmClient != nil {
		m.llmClient.SetActiveModelByID(def.Model)
	}

	m.emitSessionUpdate(sessionID)
	return nil
}

func normalizePersonaAlias(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lead", "master", "coordinator", "orchestrator":
		return "orchestrator"
	case "plan", "planner", "architect", "architecture", "planning":
		return "planning"
	case "research", "researcher", "explore", "explorer", "search", "investigator":
		return "research"
	case "code", "coder", "implement", "implementer", "developer", "coding", "default":
		return "coding"
	case "review", "reviewer", "auditor", "audit", "pr-reviewer":
		return "reviewer"
	case "test", "tester", "testing", "qa", "verifier", "verify":
		return "testing"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// FindDefinition returns a configured agent definition by ID, role, name, or persona alias.
func (m *Manager) FindDefinition(roleOrID string) (AgentDefinition, bool) {
	if roleOrID == "" {
		return AgentDefinition{}, false
	}
	trimmed := strings.TrimSpace(roleOrID)
	lower := strings.ToLower(trimmed)

	// 1. Direct ID match
	if def, ok := m.getDefinition(trimmed); ok {
		return def, true
	}
	if def, ok := m.getDefinition(lower); ok {
		return def, true
	}

	// 2. Search user-managed definitions by Role or Name
	m.mu.RLock()
	for _, d := range m.definitions {
		if strings.ToLower(string(d.Role)) == lower || strings.EqualFold(d.Name, trimmed) {
			d.Builtin = isBuiltinAgentID(d.ID)
			m.mu.RUnlock()
			return d, true
		}
	}
	m.mu.RUnlock()

	// 3. Search built-in definitions by Role or Name
	for _, b := range builtinAgentDefs() {
		if strings.ToLower(string(b.Role)) == lower || strings.EqualFold(b.Name, trimmed) {
			if userDef, ok := m.getDefinition(b.ID); ok {
				return userDef, true
			}
			return b, true
		}
	}

	// 4. Persona alias normalization (e.g. "planner" -> "planning", "coder" -> "coding")
	aliased := normalizePersonaAlias(lower)
	if aliased != lower {
		if def, ok := m.getDefinition(aliased); ok {
			return def, true
		}
	}

	return AgentDefinition{}, false
}

func (m *Manager) getDefinition(id string) (AgentDefinition, bool) {
	m.mu.RLock()
	def, ok := m.definitions[id]
	m.mu.RUnlock()
	if ok {
		def.Builtin = isBuiltinAgentID(def.ID)
		return def, true
	}
	// Fall back to built-in agents.
	for _, b := range builtinAgentDefs() {
		if b.ID == id {
			return b, true
		}
	}
	return AgentDefinition{}, false
}

func (m *Manager) saveDefinitionsLocked() {
	path := m.definitionsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(m.definitions, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}

func (m *Manager) loadDefinitions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.definitions = make(map[string]AgentDefinition)
	path := m.definitionsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// 1. Try unmarshaling as map[string]AgentDefinition (how saveDefinitionsLocked writes it)
	var asMap map[string]AgentDefinition
	if err := json.Unmarshal(data, &asMap); err == nil {
		for id, d := range asMap {
			if d.ID == "" {
				d.ID = id
			}
			m.definitions[d.ID] = d
		}
	} else {
		// 2. Fall back to unmarshaling as []AgentDefinition
		var asList []AgentDefinition
		if err := json.Unmarshal(data, &asList); err == nil {
			for _, d := range asList {
				if d.ID != "" {
					m.definitions[d.ID] = d
				}
			}
		}
	}

	// 3. Hydrate missing tools and skills for built-in archetypes
	for _, b := range builtinAgentDefs() {
		if cur, ok := m.definitions[b.ID]; ok {
			changed := false
			if len(cur.Tools) == 0 && len(b.Tools) > 0 {
				cur.Tools = b.Tools
				changed = true
			}
			if len(cur.Skills) == 0 && len(b.Skills) > 0 {
				cur.Skills = b.Skills
				changed = true
			}
			if changed {
				m.definitions[b.ID] = cur
			}
		}
	}
}

func (m *Manager) normalizeFolder(folder string) string {
	if folder != "" {
		if abs, err := filepath.Abs(folder); err == nil {
			return abs
		}
		return folder
	}
	// Fall back to the current workspace, then home — never the process
	// working directory (which is "/" for a Finder-launched app).
	if def, ok := m.defaultFolder.Load().(string); ok && def != "" {
		return def
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// ConfigChanged publishes an event so the frontend can refresh provider/model
// lists after settings change.
func (m *Manager) ConfigChanged() {
	if m.bus != nil {
		m.bus.Publish(events.Event{
			Type: "agent:config:changed",
			Data: map[string]interface{}{},
		})
	}
}
