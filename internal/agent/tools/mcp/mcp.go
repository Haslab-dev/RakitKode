// Package mcp manages Model Context Protocol client sessions and
// exposes their tools to the agent.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Haslab-dev/RakitKode/internal/config"
)

// State is the lifecycle state of an MCP server.
type State string

const (
	StateNotStarted State = "not_started"
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateFailed     State = "failed"
)

// ServerState is the observable status of one configured server.
type ServerState struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	Err   string `json:"error,omitempty"`
}

type sessionEntry struct {
	session *mcp.ClientSession
	cancel  context.CancelFunc
	tools   []*mcp.Tool
	err     error
}

var (
	mu          sync.RWMutex
	sessions    = map[string]*sessionEntry{}
	states      = map[string]ServerState{}
	initialized = make(chan struct{})
	initClose   sync.Once
)

// ArmInit marks initialization as about to run (no-op; the barrier
// channel is allocated statically). Kept for API compatibility.
func ArmInit() {}

// Initialize connects every enabled MCP server in the config. Safe to
// call multiple times: already-connected servers are skipped.
func Initialize(ctx context.Context, cfg *config.Config) {
	defer initClose.Do(func() { close(initialized) })

	for name, m := range cfg.MCP {
		if m.Disabled {
			setState(name, StateNotStarted, "disabled in config")
			continue
		}
		if m.Enabled != nil && !*m.Enabled {
			setState(name, StateNotStarted, "disabled in config")
			continue
		}
		mu.RLock()
		entry, connected := sessions[name]
		mu.RUnlock()
		if connected && entry.session != nil {
			continue
		}
		if err := connectServer(ctx, name, m); err != nil {
			slog.Error("Failed to connect MCP server", "name", name, "error", err)
			setState(name, StateFailed, err.Error())
		}
	}
}

// WaitForInit blocks until initialization finished or ctx is done.
func WaitForInit(ctx context.Context) error {
	ArmInit()
	select {
	case <-initialized:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForInitBudget bounds the wait by budget.
func WaitForInitBudget(ctx context.Context, budget time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return WaitForInit(ctx)
}

// InitWaitBudget is the default startup wait for MCP servers.
const InitWaitBudget = 10 * time.Second

// GetStates returns the current state of every configured server.
func GetStates() []ServerState {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]ServerState, 0, len(states))
	for _, s := range states {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b ServerState) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// GetTools returns the discovered tools of a connected server.
func GetTools(name string) ([]*mcp.Tool, bool) {
	mu.RLock()
	defer mu.RUnlock()
	entry, ok := sessions[name]
	if !ok || entry.session == nil {
		return nil, false
	}
	return entry.tools, true
}

// AllTools returns server-name to tools for every connected server.
func AllTools() map[string][]*mcp.Tool {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string][]*mcp.Tool, len(sessions))
	for name, entry := range sessions {
		if entry.session != nil {
			out[name] = entry.tools
		}
	}
	return out
}

// CallTool invokes one tool on a server.
func CallTool(ctx context.Context, cfg *config.Config, name, toolName string, args map[string]any) (string, error) {
	mu.RLock()
	entry, ok := sessions[name]
	mu.RUnlock()
	if !ok || entry.session == nil {
		// Try a lazy reconnect from config.
		m, hasCfg := cfg.MCP[name]
		if !hasCfg {
			return "", fmt.Errorf("MCP server %q not configured", name)
		}
		if err := connectServer(ctx, name, m); err != nil {
			return "", fmt.Errorf("MCP server %q unavailable: %w", name, err)
		}
		mu.RLock()
		entry, ok = sessions[name]
		mu.RUnlock()
		if !ok || entry.session == nil {
			return "", fmt.Errorf("MCP server %q unavailable", name)
		}
	}

	result, err := entry.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}
	return renderToolResult(result), nil
}

// CloseAll shuts down every MCP session.
func CloseAll() {
	mu.Lock()
	defer mu.Unlock()
	for name, entry := range sessions {
		if entry.cancel != nil {
			entry.cancel()
		}
		if entry.session != nil {
			_ = entry.session.Close()
		}
		delete(sessions, name)
		setStateLocked(name, StateNotStarted, "closed")
	}
}

func setState(name string, state State, errMsg string) {
	mu.Lock()
	defer mu.Unlock()
	setStateLocked(name, state, errMsg)
}

func setStateLocked(name string, state State, errMsg string) {
	states[name] = ServerState{Name: name, State: state, Err: errMsg}
}

// connectServer establishes one MCP session over stdio or HTTP.
func connectServer(ctx context.Context, name string, m config.MCPConfig) error {
	setState(name, StateConnecting, "")

	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "rakitkode",
		Version: "0.1.0",
	}, nil)

	var transport mcp.Transport
	switch m.ResolvedType() {
	case config.MCPStdio:
		command := os.ExpandEnv(m.Command)
		if strings.TrimSpace(command) == "" {
			cancel()
			return fmt.Errorf("mcp stdio config requires a non-empty 'command' field")
		}
		cmd := exec.CommandContext(sessCtx, command, m.Args...)
		env := os.Environ()
		for k, v := range m.Env {
			env = append(env, k+"="+os.ExpandEnv(v))
		}
		cmd.Env = env
		transport = &mcp.CommandTransport{Command: cmd}
	case config.MCPHttp:
		url := os.ExpandEnv(m.URL)
		if strings.TrimSpace(url) == "" {
			cancel()
			return fmt.Errorf("mcp http config requires a non-empty 'url' field")
		}
		httpClient := &http.Client{Timeout: 30 * time.Second}
		transport = &mcp.StreamableClientTransport{
			Endpoint:   url,
			HTTPClient: httpClient,
		}
	default:
		cancel()
		return fmt.Errorf("unsupported mcp type %q", m.Type)
	}

	session, err := client.Connect(sessCtx, transport, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to connect: %w", err)
	}

	toolsResp, err := session.ListTools(sessCtx, &mcp.ListToolsParams{})
	if err != nil {
		_ = session.Close()
		cancel()
		return fmt.Errorf("failed to list tools: %w", err)
	}

	mu.Lock()
	defer mu.Unlock()
	sessions[name] = &sessionEntry{
		session: session,
		cancel:  cancel,
		tools:   toolsResp.Tools,
	}
	setStateLocked(name, StateConnected, "")
	slog.Info("Connected MCP server", "name", name, "tools", len(toolsResp.Tools))
	return nil
}

// renderToolResult flattens MCP content blocks into agent-facing text.
func renderToolResult(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		switch block := c.(type) {
		case *mcp.TextContent:
			sb.WriteString(block.Text)
		case *mcp.ImageContent:
			fmt.Fprintf(&sb, "[image: %s]", block.MIMEType)
		case *mcp.AudioContent:
			fmt.Fprintf(&sb, "[audio: %s]", block.MIMEType)
		case *mcp.EmbeddedResource:
			fmt.Fprintf(&sb, "[resource: %s]", block.Resource.URI)
		default:
			sb.WriteString("[unsupported content]")
		}
		sb.WriteString("\n")
	}
	out := strings.TrimSpace(sb.String())
	if result.IsError {
		return "Error: " + out
	}
	return out
}
