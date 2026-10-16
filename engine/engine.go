// Package engine is the high-level entrypoint for embedding the rakit agent:
// it wires the LLM client, tool registry, skills, plugins, MCP, and the agent
// manager behind one constructor. Hosts that need finer control can construct
// the underlying packages directly — the engine is a convenience, not a gate.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
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

	llmClient := llm.NewLLMClient(home)
	if opts.Model != "" {
		llmClient.SetActiveModelByID(opts.Model)
	}

	toolReg := tools.NewRegistry(search.NewSearchManager())
	skillMgr := skills.NewManager("")
	skillMgr.SetWorkspace(workspace)
	pluginMgr := plugins.NewManager(home, bus)
	pluginMgr.SetWorkspace(workspace)
	mcpMgr := mcp.NewManager(home)

	mgr := agent.NewManager(llmClient, toolReg, skillMgr, pluginMgr, mcpMgr, opts.Term, bus, home)
	mgr.SetDefaultFolder(workspace)

	e := &Engine{
		bus:       bus,
		llmClient: llmClient,
		toolReg:   toolReg,
		skillMgr:  skillMgr,
		pluginMgr: pluginMgr,
		mcpMgr:    mcpMgr,
		mgr:       mgr,
		home:      home,
		workspace: workspace,
		yolo:      opts.Yolo,
	}

	if opts.ConnectMCP {
		_ = mcpMgr.ConnectAll(context.Background())
		e.RegisterMCPTools()
	}

	return e, nil
}

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
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
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

	return e.runTurn(ctx, sess.ID, prompt)
}

// RunIn executes one turn inside an existing session (REPLs, resume flows)
// and returns the assistant's final text.
func (e *Engine) RunIn(ctx context.Context, sessionID, prompt string) (string, error) {
	return e.runTurn(ctx, sessionID, prompt)
}

// runTurn subscribes to the bus, sends the message, and blocks until
// agent:turn_end for this session. Text deltas are accumulated and returned.
func (e *Engine) runTurn(ctx context.Context, sessionID, prompt string) (string, error) {
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

	if err := e.mgr.SendMessage(ctx, sessionID, prompt, nil); err != nil {
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
