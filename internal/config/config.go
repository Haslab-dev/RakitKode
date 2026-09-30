// Package config loads and validates the rakitkode.json project config.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ProviderType selects the fantasy provider implementation.
type ProviderType string

const (
	ProviderTypeOpenAI     ProviderType = "openai"
	ProviderTypeAnthropic  ProviderType = "anthropic"
	ProviderTypeGoogle     ProviderType = "google"
	ProviderTypeOpenAICompat ProviderType = "openai-compat"
)

// SelectedModel points at a model on a configured provider.
type SelectedModel struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

// ProviderConfig describes one LLM provider.
type ProviderConfig struct {
	ID          string       `json:"id,omitempty"`
	Name        string       `json:"name,omitempty"`
	Type        ProviderType `json:"type,omitempty"`
	BaseURL     string       `json:"base_url,omitempty"`
	APIKey      string       `json:"api_key,omitempty"`
	APIKeyEnv   string       `json:"api_key_env,omitempty"`
	Models      []Model      `json:"models"`
	ExtraParams map[string]any `json:"extra_params,omitempty"`
	Disabled    bool         `json:"disabled,omitempty"`
}

// Model metadata mirrors the subset of catwalk.Model the harness needs.
type Model struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ContextWindow    int64  `json:"context_window"`
	DefaultMaxTokens int64  `json:"default_max_tokens"`
	CanReason        bool   `json:"can_reason"`
	SupportsImages   bool   `json:"supports_attachments"`
	CostPer1MIn      float64 `json:"cost_per_1m_in,omitempty"`
	CostPer1MOut     float64 `json:"cost_per_1m_out,omitempty"`
}

// MCPConfig describes one MCP server (stdio or http).
type MCPConfig struct {
	Type     MCPType          `json:"type,omitempty"`
	Command  string           `json:"command,omitempty"`
	Args     []string         `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string           `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Enabled  *bool            `json:"enabled,omitempty"`
	Disabled bool             `json:"disabled,omitempty"`
}

// MCPType is the transport type of an MCP server.
type MCPType string

const (
	MCPStdio MCPType = "stdio"
	MCPHttp  MCPType = "http"
)

// IsEmpty reports whether the config carries no transport info.
func (m MCPConfig) IsEmpty() bool {
	return m.Type == "" && m.Command == "" && m.URL == ""
}

// ResolvedType returns the transport type, defaulting to stdio when a
// command is present.
func (m MCPConfig) ResolvedType() MCPType {
	if m.Type != "" {
		return m.Type
	}
	if m.Command != "" {
		return MCPStdio
	}
	if m.URL != "" {
		return MCPHttp
	}
	return MCPStdio
}

// Options are general application options.
type Options struct {
	ContextPaths         []string `json:"context_paths,omitempty"`
	GlobalContextPaths   []string `json:"global_context_paths,omitempty"`
	SkillsPaths          []string `json:"skills_paths,omitempty"`
	DataDirectory        string   `json:"data_directory,omitempty"`
	DisableAutoSummarize bool     `json:"disable_auto_summarize,omitempty"`
	SkipPermissions      bool     `json:"skip_permissions,omitempty"`
	Debug                bool     `json:"debug,omitempty"`
}

// Config is the whole rakitkode.json document.
type Config struct {
	Schema     string                     `json:"$schema,omitempty"`
	Models     map[SelectedModelType]SelectedModel `json:"models,omitempty"`
	Providers  map[string]ProviderConfig  `json:"providers,omitempty"`
	MCP        map[string]MCPConfig       `json:"mcp,omitempty"`
	Options    *Options                   `json:"options,omitempty"`
	Env        map[string]string          `json:"env,omitempty"`
}

// SelectedModelType is which slot a model fills.
type SelectedModelType string

const (
	SelectedModelTypeLarge SelectedModelType = "large"
	SelectedModelTypeSmall SelectedModelType = "small"
)

// DataDir returns the data directory, defaulting to .rakitkode.
func (c *Config) DataDir() string {
	if c.Options != nil && c.Options.DataDirectory != "" {
		return c.Options.DataDirectory
	}
	return ".rakitkode"
}

// GetModel looks up a model on a provider.
func (c *Config) GetModel(providerID, modelID string) *Model {
	p, ok := c.Providers[providerID]
	if !ok {
		return nil
	}
	for _, m := range p.Models {
		if m.ID == modelID {
			return &m
		}
	}
	return nil
}

// Store wraps the loaded config with its working directory.
type Store struct {
	cfg        *Config
	workingDir string
}

// NewStore builds a Store around an already-loaded config.
func NewStore(cfg *Config, workingDir string) *Store {
	if cfg.Options == nil {
		cfg.Options = &Options{}
	}
	if !filepath.IsAbs(cfg.DataDir()) {
		cfg.Options.DataDirectory = filepath.Join(workingDir, cfg.DataDir())
	}
	return &Store{cfg: cfg, workingDir: workingDir}
}

// Config returns the current config snapshot.
func (s *Store) Config() *Config { return s.cfg }

// WorkingDir returns the project working directory.
func (s *Store) WorkingDir() string { return s.workingDir }

// Resolve expands $VAR references in config values via the environment.
func (s *Store) Resolve(v string) (string, error) {
	return os.ExpandEnv(v), nil
}

// ConfigFileNames are the config files looked up, in priority order.
var ConfigFileNames = []string{"rakitkode.json", ".rakitkode.json"}

// Load discovers and loads the project config. A missing config file is
// not an error; an empty default is returned so onboarding can proceed.
func Load(workingDir string) (*Store, error) {
	dir := workingDir
	for {
		for _, name := range ConfigFileNames {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				cfg, err := loadFile(path)
				if err != nil {
					return nil, fmt.Errorf("failed to load %s: %w", path, err)
				}
				applyEnv(cfg)
				return NewStore(cfg, workingDir), nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	applyEnv(&Config{})
	return NewStore(&Config{Options: &Options{}}, workingDir), nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	return &cfg, nil
}

// applyEnv sets config env vars into the process environment so
// provider API key references like $MY_KEY resolve.
func applyEnv(cfg *Config) {
	for k, v := range cfg.Env {
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}

// EnabledProviders returns non-disabled providers sorted by ID.
func (c *Config) EnabledProviders() []ProviderConfig {
	var out []ProviderConfig
	for id, p := range c.Providers {
		if p.ID == "" {
			p.ID = id
		}
		if !p.Disabled {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
