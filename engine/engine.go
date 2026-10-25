// Package engine is the high-level entrypoint for embedding the rakit agent:
// it wires the LLM client, tool registry, skills, plugins, MCP, and the agent
// manager behind one constructor. Hosts that need finer control can construct
// the underlying packages directly — the engine is a convenience, not a gate.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/hooks"
	"github.com/Haslab-dev/RakitKode/index"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
	"github.com/Haslab-dev/RakitKode/memory"
	"github.com/Haslab-dev/RakitKode/permission"
	"github.com/Haslab-dev/RakitKode/plugins"
	"github.com/Haslab-dev/RakitKode/search"
	"github.com/Haslab-dev/RakitKode/skills"
	"github.com/Haslab-dev/RakitKode/store"
	"github.com/Haslab-dev/RakitKode/tools"
)

// agentUpdatedEvent is emitted by the manager as a raw string (no const yet).
const agentUpdatedEvent = events.EventType("agent:updated")

// Options configures a new Engine.
type Options struct {
	// Home is the engine's storage root. Empty means $RAKIT_HOME, else
	// ~/.rakit. Provider profiles, MCP servers, plugins, sessions, and skills
	// all live under it.
	Home string

	// Workspace is the folder the agent operates on. Empty means the current
	// process working directory.
	Workspace string

	// Model selects the initial model as "provider/model" (for example
	// "openai/gpt-4o"). Empty uses the client's active profile.
	Model string

	// Yolo auto-approves every tool call. Only for trusted, sandboxed use.
	Yolo bool

	// Bus lets a host supply its own event bus (subscribe before New returns
	// to catch everything). A new bus is created when nil.
	Bus *events.Bus

	// Term optionally supplies persistent shells (PTY-backed). nil makes the
	// bash tool run one-shot subprocesses.
	Term agent.ShellManager

	// ConnectMCP connects configured MCP servers at startup and registers
	// their tools. Off by default: hosts usually want lazy or custom wiring.
	ConnectMCP bool

	// Optional pre-built components. When nil (the normal case) the engine
	// constructs its own from Home; hosts that need custom construction —
	// a desktop app sharing its own tool registry or search index, a server
	// with its own LLM profile store — supply them and the engine adopts
	// them as-is. See CONTRACT.md.
	LLMClient *llm.LLMClient
	Tools     *tools.Registry
	Skills    *skills.Manager
	Plugins   *plugins.Manager
	MCP       *mcp.Manager
	Memory    *memory.Manager
}

// Engine is a wired, ready-to-use agent runtime.
type Engine struct {
	bus       *events.Bus
	llmClient *llm.LLMClient
	toolReg   *tools.Registry
	skillMgr  *skills.Manager
	pluginMgr *plugins.Manager
	mcpMgr    *mcp.Manager
	mgr       *agent.Manager
	idxStore  *index.Store
	home      string
	workspace string
	yolo      bool
}

// New assembles the engine. It returns an error only when the workspace or
// home cannot be resolved; LLM configuration problems surface on first use.
func New(opts Options) (*Engine, error) {
	workspace := opts.Workspace
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve workspace: %w", err)
		}
	}
	if opts.Home != "" {
		store.SetRoot(opts.Home)
	}
	home, err := store.Root()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}

	bus := opts.Bus
	if bus == nil {
		bus = events.NewBus()
	}

	llmClient := opts.LLMClient
	if llmClient == nil {
		llmClient = llm.NewLLMClient(home)
	}
	if opts.Model != "" {
		llmClient.SetActiveModelByID(opts.Model)
	}

	toolReg := opts.Tools
	if toolReg == nil {
		searchMgr := search.NewSearchManager()
		searchMgr.SetDirectories([]string{workspace})
		toolReg = tools.NewRegistry(searchMgr)
	}
	skillMgr := opts.Skills
	if skillMgr == nil {
		skillMgr = skills.NewManager("")
		skillMgr.SetWorkspace(workspace)
	}
	pluginMgr := opts.Plugins
	if pluginMgr == nil {
		pluginMgr = plugins.NewManager(home, bus)
		pluginMgr.SetWorkspace(workspace)
	}
	mcpMgr := opts.MCP
	if mcpMgr == nil {
		mcpMgr = mcp.NewManager(home)
	}

	permSettings, err := permission.LoadSettings(permission.DefaultSettingsPaths(home, workspace)...)
	if err != nil {
		log.Printf("[engine] permissions: %v", err)
	}
	hookCfg, err := hooks.Load(hooks.DefaultPaths(home, workspace)...)
	if err != nil {
		log.Printf("[engine] hooks: %v", err)
	}
	hookRunner := hooks.NewRunner(hookCfg, workspace)

	// Workspace symbol index. It is built lazily on the first symbol_search
	// call so engine construction stays fast.
	idxStore := index.New(workspace)
	var idxOnce sync.Once
	ensureIndex := func() {
		idxOnce.Do(func() {
			if err := idxStore.Build(); err != nil {
				log.Printf("[engine] index build: %v", err)
			}
		})
	}
	if toolReg != nil {
		if _, exists := toolReg.Lookup("symbol_search"); !exists {
			toolReg.Register(tools.ToolSpec{
				Name:            "symbol_search",
				Description:     "Find declarations (functions, types, classes) by name in the workspace index.",
				Cost:            "cheap",
				ReadOnly:        true,
				SideEffectScope: "none",
				Category:        "search",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "Symbol name or prefix"},
						"limit": map[string]any{"type": "integer", "description": "Max results (default 50)"},
					},
					"required": []string{"query"},
				},
				Handler: func(ctx context.Context, args map[string]any) (any, error) {
					query, _ := args["query"].(string)
					query = strings.TrimSpace(query)
					if query == "" {
						return nil, fmt.Errorf("query is required")
					}
					ensureIndex()
					limit := 50
					if v, ok := args["limit"].(float64); ok && v > 0 {
						limit = int(v)
					}
					syms := idxStore.Find(query)
					if len(syms) == 0 {
						lower := strings.ToLower(query)
						for _, s := range idxStore.Symbols() {
							if strings.HasPrefix(strings.ToLower(s.Name), lower) {
								syms = append(syms, s)
								if len(syms) >= limit {
									break
								}
							}
						}
					}
					if len(syms) > limit {
						syms = syms[:limit]
					}
					out := make([]map[string]any, 0, len(syms))
					for _, s := range syms {
						out = append(out, map[string]any{
							"name":     s.Name,
							"kind":     s.Kind.String(),
							"file":     s.File,
							"line":     s.Line,
							"column":   s.Column,
							"scope":    s.Scope,
							"exported": s.Exported,
						})
					}
					return map[string]any{"query": query, "symbols": out, "count": len(out)}, nil
				},
			})
		}
	}

	mgr := agent.NewManagerWithOptions(agent.ManagerOptions{
		LLMClient:    llmClient,
		ToolRegistry: toolReg,
		Skills:       skillMgr,
		Plugins:      pluginMgr,
		MCP:          mcpMgr,
		Shell:        opts.Term,
		Bus:          bus,
		DataDir:      home,
		Memory:       opts.Memory,
		Permissions:  permSettings,
		Hooks:        hookRunner,
		Workspace:    workspace,
	})
	mgr.SetDefaultFolder(workspace)

	e := &Engine{
		bus:       bus,
		llmClient: llmClient,
		toolReg:   toolReg,
		skillMgr:  skillMgr,
		pluginMgr: pluginMgr,
		mcpMgr:    mcpMgr,
		mgr:       mgr,
		idxStore:  idxStore,
		home:      home,
		workspace: workspace,
		yolo:      opts.Yolo,
	}

	if opts.ConnectMCP {
		if err := mcpMgr.ConnectAll(context.Background()); err != nil {
			log.Printf("[engine] mcp connect: %v", err)
		}
		for _, s := range mcpMgr.ListServers() {
			if s.Enabled && s.Status == "error" {
				log.Printf("[engine] mcp server %q failed: %s", s.Name, s.LastError)
			}
		}
		e.RegisterMCPTools()
	}

	return e, nil
}

// SetPermissionMode overrides the collaboration mode for one session.
func (e *Engine) SetPermissionMode(sessionID, mode string) error {
	return e.mgr.SetPermissionMode(sessionID, mode)
}

// PermissionModeFor returns the effective permission mode for a session.
func (e *Engine) PermissionModeFor(sessionID string) string {
	return e.mgr.PermissionModeFor(sessionID)
}

// UndoLast restores the most recent file checkpoint for a session.
func (e *Engine) UndoLast(sessionID string) error {
	return e.mgr.UndoLast(sessionID)
}

// ResumeTurn resumes an interrupted turn recovered from the journal.
func (e *Engine) ResumeTurn(sessionID string) error {
	return e.mgr.ResumeTurn(sessionID)
}

// Index returns the workspace symbol index.
func (e *Engine) Index() *index.Store { return e.idxStore }

// RegisterMCPTools connects nothing but registers whatever tools the MCP
// manager reports as connected into the engine's tool registry.
func (e *Engine) RegisterMCPTools() {
	connected := e.mcpMgr.ListConnectedTools()
	if len(connected) == 0 {
		return
	}
	e.toolReg.SetMCPCaller(callerFunc(e.mcpMgr.CallTool))
	for _, t := range connected {
		e.toolReg.RegisterMCPToolWithCaller(llm.MCPTool{
			ServerName:  t.ServerName,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Title:       t.Title,
			ReadOnly:    t.ReadOnly,
			Destructive: t.Destructive,
		}, callerFunc(func(ctx context.Context, fullName string, args map[string]any) (string, error) {
			return e.mcpMgr.CallTool(ctx, fullName, args)
		}))
	}
}

type callerFunc func(ctx context.Context, name string, args map[string]any) (string, error)

func (f callerFunc) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	return f(ctx, name, args)
}

// Manager exposes the agent manager for full control (sessions, approvals,
// subagents, schedules).
func (e *Engine) Manager() *agent.Manager { return e.mgr }

// LLM returns the engine's LLM client (provider profiles, active model,
// chat APIs).
func (e *Engine) LLM() *llm.LLMClient { return e.llmClient }

// Tools returns the engine's tool registry; hosts register app-specific
// tools and MCP tools into it.
func (e *Engine) Tools() *tools.Registry { return e.toolReg }

// MCP returns the engine's MCP manager (server config, connect/disconnect,
// connected tools).
func (e *Engine) MCP() *mcp.Manager { return e.mcpMgr }

// Plugins returns the engine's plugin manager.
func (e *Engine) Plugins() *plugins.Manager { return e.pluginMgr }

// SkillManager returns the engine's skills manager (Skills lists the
// discovered skills).
func (e *Engine) SkillManager() *skills.Manager { return e.skillMgr }

// Memory returns the manager's memory instance.
func (e *Engine) Memory() *memory.Manager { return e.mgr.MemoryManager() }

// Bus exposes the event bus; all agent activity streams through it.
func (e *Engine) Bus() *events.Bus { return e.bus }

// Home is the resolved storage root.
func (e *Engine) Home() string { return e.home }

// Workspace is the resolved working folder.
func (e *Engine) Workspace() string { return e.workspace }

// ErrNoProvider is returned by Run when no provider profile or environment
// API key is available.
var ErrNoProvider = errors.New("no LLM provider configured: run `rakit import`, set an API key env var (e.g. OPENAI_API_KEY), or add a profile under the home dir")

// Run executes one headless turn in a throwaway session and returns the
// assistant's final text. Mutating tool calls are auto-approved only when
// Options.Yolo was set; otherwise they are declined so the turn can end.
func (e *Engine) Run(ctx context.Context, prompt string) (string, error) {
	if !e.hasProvider() {
		return "", ErrNoProvider
	}
	sess, err := e.mgr.CreateSession("rakit", agent.RoleCoding, e.workspace)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer e.mgr.DeleteSession(sess.ID)

	if e.yolo {
		_ = e.mgr.SetAutoApprove(sess.ID, true)
	}

	return e.runTurn(ctx, sess.ID, prompt, nil)
}

// RunIn executes one turn inside an existing session (REPLs, resume flows)
// and returns the assistant's final text. Optional mentioned paths are
// @-mention files whose contents get inlined for the model.
func (e *Engine) RunIn(ctx context.Context, sessionID, prompt string, mentionedPaths ...string) (string, error) {
	return e.runTurn(ctx, sessionID, prompt, mentionedPaths)
}

// runTurn subscribes to the bus, sends the message, and blocks until
// agent:turn_end for this session. Text deltas are accumulated and returned.
func (e *Engine) runTurn(ctx context.Context, sessionID, prompt string, mentionedPaths []string) (string, error) {
	var (
		mu      sync.Mutex
		text    strings.Builder
		turnErr error
		done    = make(chan struct{})
	)

	onDelta := e.bus.Subscribe(events.AgentMessageDelta, func(ev events.Event) {
		if ev.Data["session_id"] != sessionID {
			return
		}
		if kind, _ := ev.Data["kind"].(string); kind == "text" {
			if delta, _ := ev.Data["delta"].(string); delta != "" {
				mu.Lock()
				text.WriteString(delta)
				mu.Unlock()
			}
		}
	})
	defer onDelta()

	onEnd := e.bus.Subscribe(events.AgentTurnEnd, func(ev events.Event) {
		if ev.Data["session_id"] != sessionID {
			return
		}
		msg, _ := ev.Data["error"].(string)
		mu.Lock()
		if msg != "" {
			turnErr = errors.New(msg)
		}
		mu.Unlock()
		close(done)
	})
	defer onEnd()

	// Headless hosts have nobody to ask; decline so the turn can end. The
	// Manager publishes agent events while holding its own lock (e.g.
	// sessionBridge.Ask/SetTodos), so these handlers must not call back into
	// the Manager on the publisher's goroutine — that re-enters Manager.mu
	// and deadlocks the turn. Defer to another goroutine instead.
	onAsk := e.bus.Subscribe(events.AgentAsk, func(ev events.Event) {
		if ev.Data["session_id"] != sessionID {
			return
		}
		go func() { _ = e.mgr.RespondAsk(sessionID, map[string]any{}) }()
	})
	defer onAsk()

	// Nobody to approve in headless mode (unless Yolo): decline and let the
	// model adapt or end the turn.
	onAwait := e.bus.Subscribe(agentUpdatedEvent, func(ev events.Event) {
		if ev.Data["session_id"] != sessionID {
			return
		}
		go func() {
			if sess, ok := e.mgr.GetSession(sessionID); ok && sess.State == agent.StateAwaiting {
				_ = e.mgr.RespondApproval(ctx, sessionID, false, false)
			}
		}()
	})
	defer onAwait()

	if err := e.mgr.SendMessage(ctx, sessionID, prompt, mentionedPaths); err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		e.mgr.StopTurn(sessionID)
		mu.Lock()
		defer mu.Unlock()
		return text.String(), ctx.Err()
	}

	mu.Lock()
	defer mu.Unlock()
	return text.String(), turnErr
}

// HasProvider reports whether any LLM provider is usable: an active profile
// with a key (or a local base URL), or any enabled profile.
func (e *Engine) HasProvider() bool { return e.hasProvider() }

// ModelInfo returns the active provider id and model name.
func (e *Engine) ModelInfo() (string, string) {
	cfg := e.llmClient.GetConfig()
	return cfg.ProviderID, cfg.Model
}

// SetModel switches the active provider/model by full id ("provider/model").
// Reports whether a matching profile was found.
func (e *Engine) SetModel(fullID string) bool {
	for _, p := range e.llmClient.GetProviderProfiles() {
		if p.ID == fullID || strings.HasPrefix(fullID, p.ID+"/") {
			e.llmClient.SetActiveModelByID(fullID)
			return true
		}
	}
	return false
}

// Profiles lists the configured provider profiles.
func (e *Engine) Profiles() []llm.ProviderProfile { return e.llmClient.GetProviderProfiles() }

// Compact summarizes a session's history in place to free context window.
func (e *Engine) Compact(sessionID string) error {
	_, err := e.mgr.CompactSession(sessionID)
	return err
}

// UsageSummary aggregates token usage across sessions, providers, and models.
func (e *Engine) UsageSummary() map[string]any { return e.mgr.GetUsageSummary() }

// Skills lists installed skills.
func (e *Engine) Skills() []skills.Skill {
	if e.skillMgr == nil {
		return nil
	}
	return e.skillMgr.List()
}

// MCPStatus lists configured MCP servers with their connection status.
func (e *Engine) MCPStatus() []mcp.ServerConfig { return e.mcpMgr.ListServers() }

// ReasoningEffort sets the thinking-effort hint sent to providers that
// support it ("low", "medium", "high"; "" clears it).
func (e *Engine) ReasoningEffort(effort string) { e.llmClient.SetActiveReasoningEffort(effort) }

func (e *Engine) hasProvider() bool {
	cfg := e.llmClient.GetConfig()
	if cfg.Model != "" && (cfg.APIKey != "" || isLocalBaseURL(cfg.BaseURL)) {
		return true
	}
	for _, p := range e.llmClient.GetProviderProfiles() {
		if p.Enabled && (p.APIKey != "" || isLocalBaseURL(p.BaseURL)) {
			return true
		}
	}
	return false
}

func isLocalBaseURL(raw string) bool {
	if raw == "" {
		return false
	}
	return strings.Contains(raw, "localhost") || strings.Contains(raw, "127.0.0.1")
}
