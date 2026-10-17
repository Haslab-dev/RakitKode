// Package agentconfig implements the canonical agent configuration standard:
// one source of truth for Providers, Models and MCP Servers, plus adapters
// that generate each supported coding agent's native configuration
// (OpenCode, Pi, Oh My Pi, Claude Code, Codex) without clobbering settings
// the adapters do not manage.
//
// Canonical file: ~/.rakit/agent-config.json (mode 0600 —
// literal API keys are allowed; apiKeyEnv references are preferred).
//
// Merge rules (CONTRACT: "merge, don't clobber"): adapters only write the
// sections they own. Existing entries in native files that the canonical
// config does not know about (per-agent servers, unrelated settings) are
// preserved untouched.
package agentconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Version is the canonical schema version.
const Version = 1

// Transport constants for MCP servers.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// Protocol constants for providers.
const (
	ProtocolChatCompletions   = "chat-completions"
	ProtocolResponses         = "responses"
	ProtocolAnthropicMessages = "anthropic-messages"
)

// Config is the canonical configuration schema.
type Config struct {
	Version    int                  `json:"version"`
	Providers  map[string]Provider  `json:"providers"`
	Models     map[string]Model     `json:"models"`
	MCPServers map[string]MCPServer `json:"mcpServers"`
	Defaults   Defaults             `json:"defaults,omitempty"`
}

// Provider is one upstream API gateway.
type Provider struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // openai-compatible | anthropic
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey,omitempty"`    // literal; file must stay 0600
	APIKeyEnv string `json:"apiKeyEnv,omitempty"` // env var name (preferred)
	Protocol  string `json:"protocol,omitempty"`  // chat-completions | responses | anthropic-messages
}

// Model is a named model offered by a provider. The full identifier is
// "<provider>/<id>".
type Model struct {
	Provider        string `json:"provider"`
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	ContextWindow   int    `json:"contextWindow,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	Reasoning       bool   `json:"reasoning,omitempty"`
}

// MCPServer is one Model Context Protocol server.
type MCPServer struct {
	Transport string            `json:"transport"` // stdio | http | sse
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

// Defaults holds cross-agent default selections.
type Defaults struct {
	Model string `json:"model,omitempty"` // "<provider>/<id>"
}

// DefaultPath returns the canonical config path: the engine home
// (~/.rakit, matching the rakit CLI), which acts as the base layer that
// host apps may override with their own config.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rakit", "agent-config.json"), nil
}

// Load reads and parses the canonical config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	if c.Models == nil {
		c.Models = map[string]Model{}
	}
	if c.MCPServers == nil {
		c.MCPServers = map[string]MCPServer{}
	}
	return &c, nil
}

// Save writes the canonical config with 0600 permissions (literal API keys
// may be present).
func Save(path string, c *Config) error {
	c.Version = Version
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Validate checks referential integrity of the canonical config.
func (c *Config) Validate() error {
	for name, m := range c.Models {
		if _, ok := c.Providers[m.Provider]; !ok {
			return fmt.Errorf("model %q references unknown provider %q", name, m.Provider)
		}
	}
	if c.Defaults.Model != "" {
		p, id, ok := strings.Cut(c.Defaults.Model, "/")
		if !ok {
			return fmt.Errorf("defaults.model %q must be <provider>/<id>", c.Defaults.Model)
		}
		if _, ok := c.Providers[p]; !ok {
			return fmt.Errorf("defaults.model references unknown provider %q", p)
		}
		if _, ok := c.Models[c.Defaults.Model]; !ok && c.Models != nil {
			// bare provider/model pairs that were never named are allowed
			_ = id
		}
	}
	for name, s := range c.MCPServers {
		switch s.Transport {
		case TransportStdio:
			if s.Command == "" {
				return fmt.Errorf("mcp server %q: stdio transport needs command", name)
			}
		case TransportHTTP, TransportSSE:
			if s.URL == "" {
				return fmt.Errorf("mcp server %q: %s transport needs url", name, s.Transport)
			}
		default:
			return fmt.Errorf("mcp server %q: unknown transport %q", name, s.Transport)
		}
	}
	return nil
}

// Enabled returns whether the server is enabled (nil = true).
func (s MCPServer) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// SortedModelIDs returns full "provider/id" identifiers in stable order.
func (c *Config) SortedModelIDs() []string {
	ids := make([]string, 0, len(c.Models))
	for full := range c.Models {
		ids = append(ids, full)
	}
	sort.Strings(ids)
	return ids
}
