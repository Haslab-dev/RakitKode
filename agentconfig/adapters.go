package agentconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Targets supported by the adapters.
const (
	TargetOpenCode = "opencode"
	TargetPi       = "pi"
	TargetOMP      = "omp"
	TargetClaude   = "claude"
	TargetCodex    = "codex"
)

// AllTargets lists every supported agent, stable order.
func AllTargets() []string {
	return []string{TargetOpenCode, TargetPi, TargetOMP, TargetClaude, TargetCodex}
}

// OutFile is one native config file an adapter wants written.
type OutFile struct {
	Path    string
	Content []byte
}

// Render generates the native configuration files for one target. existing
// maps target-relative well-known paths (absolute) to their current bytes so
// adapters can merge instead of clobber. Notes carry human-readable warnings
// (skipped servers, protocol caveats).
func (c *Config) Render(target, home string) ([]OutFile, []string, error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	switch target {
	case TargetOpenCode:
		return c.renderOpenCode(home)
	case TargetPi:
		return c.renderPi(home)
	case TargetOMP:
		return c.renderOMP(home)
	case TargetClaude:
		return c.renderClaude(home)
	case TargetCodex:
		return c.renderCodex(home)
	default:
		return nil, nil, fmt.Errorf("unknown target %q (want: %s)", target, strings.Join(AllTargets(), ", "))
	}
}

// --- shared helpers ---

// jsonMap decodes JSON bytes into a generic map (missing/empty file → empty map).
func jsonMap(data []byte) (map[string]any, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, nil
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	return root, nil
}

func encodeJSON(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// subMap returns root[name] as a map, creating it when absent.
func subMap(root map[string]any, name string) map[string]any {
	if m, ok := root[name].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	root[name] = m
	return m
}

// mergeStringMaps overlays overlay onto base, returning a new map.
func mergeStringMaps(base map[string]string, overlay map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// apiKeyRef returns the literal key or a "$ENV" style reference.
func (p Provider) apiKeyValue(refStyle func(string) string) string {
	if p.APIKeyEnv != "" {
		if refStyle != nil {
			return refStyle(p.APIKeyEnv)
		}
		return "$" + p.APIKeyEnv
	}
	return p.APIKey
}

// orderedProviderIDs gives stable iteration over providers.
func (c *Config) orderedProviderIDs() []string {
	ids := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// modelsOf groups canonical models by provider id.
func (c *Config) modelsOf(providerID string) []Model {
	var out []Model
	for _, m := range c.Models {
		if m.Provider == providerID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *Config) defaultProviderID() string {
	if c.Defaults.Model == "" {
		return ""
	}
	p, _, _ := strings.Cut(c.Defaults.Model, "/")
	return p
}

func (c *Config) defaultModelID() string {
	if c.Defaults.Model == "" {
		return ""
	}
	_, id, _ := strings.Cut(c.Defaults.Model, "/")
	return id
}

// --- OpenCode (~/.config/opencode/opencode.json) ---

func (c *Config) renderOpenCode(home string) ([]OutFile, []string, error) {
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	root, err := jsonMap(existing)
	if err != nil {
		return nil, nil, fmt.Errorf("parse existing %s (JSONC comments are not preserved): %w", path, err)
	}

	if c.Defaults.Model != "" {
		root["model"] = c.Defaults.Model
	}
	providers := subMap(root, "provider")
	for _, id := range c.orderedProviderIDs() {
		p := c.Providers[id]
		entry := map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"name":    p.Name,
			"options": map[string]any{"baseURL": p.BaseURL, "apiKey": p.apiKeyValue(func(env string) string { return "{env:" + env + "}" })},
		}
		if p.Protocol == ProtocolResponses {
			entry["npm"] = "@ai-sdk/openai"
		}
		models := map[string]any{}
		for _, m := range c.modelsOf(id) {
			entryModel := map[string]any{}
			if m.Name != "" {
				entryModel["name"] = m.Name
			}
			limits := map[string]int{}
			if m.ContextWindow > 0 {
				limits["context"] = m.ContextWindow
			}
			if m.MaxOutputTokens > 0 {
				limits["output"] = m.MaxOutputTokens
			}
			if len(limits) > 0 {
				entryModel["limit"] = limits
			}
			models[m.ID] = entryModel
		}
		entry["models"] = models
		providers[id] = entry
	}

	mcp := subMap(root, "mcp")
	var notes []string
	for _, name := range c.sortedMCPServerNames() {
		s := c.MCPServers[name]
		switch s.Transport {
		case TransportStdio:
			entry := map[string]any{
				"type":    "local",
				"command": append([]string{s.Command}, s.Args...),
				"enabled": s.IsEnabled(),
			}
			if len(s.Env) > 0 {
				entry["environment"] = s.Env
			}
			mcp[name] = entry
		case TransportHTTP, TransportSSE:
			entry := map[string]any{"type": "remote", "url": s.URL, "enabled": s.IsEnabled()}
			if len(s.Headers) > 0 {
				entry["headers"] = s.Headers
			}
			mcp[name] = entry
		}
	}

	content, err := encodeJSON(root)
	if err != nil {
		return nil, nil, err
	}
	_ = notes
	return []OutFile{{Path: path, Content: content}}, notes, nil
}

// --- Pi (~/.pi/agent/models.json + mcp.json) ---

func (c *Config) renderPi(home string) ([]OutFile, []string, error) {
	var notes []string
	agentDir := filepath.Join(home, ".pi", "agent")

	providers := map[string]any{}
	for _, id := range c.orderedProviderIDs() {
		p := c.Providers[id]
		api := "openai-completions"
		if p.Protocol == ProtocolAnthropicMessages {
			api = "anthropic-messages"
		}
		models := []map[string]any{}
		for _, m := range c.modelsOf(id) {
			entry := map[string]any{"id": m.ID, "reasoning": m.Reasoning}
			if m.Name != "" {
				entry["name"] = m.Name
			}
			if m.ContextWindow > 0 {
				entry["contextWindow"] = m.ContextWindow
			}
			if m.MaxOutputTokens > 0 {
				entry["maxTokens"] = m.MaxOutputTokens
			}
			models = append(models, entry)
		}
		providers[id] = map[string]any{
			"name":    p.Name,
			"baseUrl": p.BaseURL,
			"api":     api,
			"apiKey":  p.apiKeyValue(nil), // "$ENV" or literal
			"models":  models,
		}
	}
	modelsJSON, err := encodeJSON(map[string]any{"providers": providers})
	if err != nil {
		return nil, nil, err
	}

	// Pi's mcp.json supports stdio servers only; HTTP/SSE servers are noted.
	mcpPath := filepath.Join(agentDir, "mcp.json")
	existing, err := os.ReadFile(mcpPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	root, err := jsonMap(existing)
	if err != nil {
		return nil, nil, err
	}
	servers := subMap(root, "mcpServers")
	for _, name := range c.sortedMCPServerNames() {
		s := c.MCPServers[name]
		if !s.IsEnabled() {
			continue // disabled: leave out of the native config entirely
		}
		if s.Transport != TransportStdio {
			notes = append(notes, fmt.Sprintf("mcp server %q skipped (%s transport not supported by pi)", name, s.Transport))
			continue
		}
		entry := map[string]any{"command": s.Command, "args": s.Args}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		servers[name] = entry
	}
	mcpJSON, err := encodeJSON(root)
	if err != nil {
		return nil, nil, err
	}

	return []OutFile{
		{Path: filepath.Join(agentDir, "models.json"), Content: modelsJSON},
		{Path: mcpPath, Content: mcpJSON},
	}, notes, nil
}

// --- Oh My Pi (~/.omp/agent/models.yml + mcp.json) ---

func (c *Config) renderOMP(home string) ([]OutFile, []string, error) {
	var notes []string
	agentDir := filepath.Join(home, ".omp", "agent")

	providers := map[string]any{}
	for _, id := range c.orderedProviderIDs() {
		p := c.Providers[id]
		api := "openai-completions"
		if p.Protocol == ProtocolAnthropicMessages {
			api = "anthropic-messages"
		}
		models := []map[string]any{}
		for _, m := range c.modelsOf(id) {
			entry := map[string]any{"id": m.ID}
			if m.Name != "" {
				entry["name"] = m.Name
			}
			if m.ContextWindow > 0 {
				entry["contextWindow"] = m.ContextWindow
			}
			if m.MaxOutputTokens > 0 {
				entry["maxTokens"] = m.MaxOutputTokens
			}
			if m.Reasoning {
				entry["reasoning"] = true
			}
			models = append(models, entry)
		}
		entry := map[string]any{
			"baseUrl": p.BaseURL,
			"api":     api,
			"apiKey":  p.apiKeyValue(nil), // omp resolves env names first, literal fallback
			"models":  models,
		}
		if p.Name != "" {
			entry["name"] = p.Name
		}
		providers[id] = entry
	}
	modelsYAML, err := yaml.Marshal(map[string]any{"providers": providers})
	if err != nil {
		return nil, nil, err
	}

	// omp mcp.json: stdio servers; http/sse noted as unsupported.
	mcpPath := filepath.Join(agentDir, "mcp.json")
	existing, err := os.ReadFile(mcpPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	root, err := jsonMap(existing)
	if err != nil {
		return nil, nil, err
	}
	servers := subMap(root, "mcpServers")
	var disabled []string
	for _, name := range c.sortedMCPServerNames() {
		s := c.MCPServers[name]
		if s.Transport != TransportStdio {
			notes = append(notes, fmt.Sprintf("mcp server %q skipped (%s transport not supported by omp)", name, s.Transport))
			continue
		}
		if !s.IsEnabled() {
			disabled = append(disabled, name)
			continue
		}
		entry := map[string]any{"type": "stdio", "command": s.Command, "args": s.Args}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		servers[name] = entry
	}
	if existingDisabled, ok := root["disabledServers"].([]any); ok && len(disabled) == 0 {
		root["disabledServers"] = existingDisabled
	} else if len(disabled) > 0 {
		root["disabledServers"] = disabled
	}
	mcpJSON, err := encodeJSON(root)
	if err != nil {
		return nil, nil, err
	}

	return []OutFile{
		{Path: filepath.Join(agentDir, "models.yml"), Content: modelsYAML},
		{Path: mcpPath, Content: mcpJSON},
	}, notes, nil
}

// --- Claude Code (~/.claude/settings.json + ~/.claude.json mcp / .mcp.json) ---

func (c *Config) renderClaude(home string) ([]OutFile, []string, error) {
	var notes []string
	var files []OutFile

	// settings.json: only meaningful when a default provider speaks the
	// Anthropic Messages protocol; OpenAI-compatible gateways are not
	// natively understood by Claude Code.
	if pID := c.defaultProviderID(); pID != "" {
		p := c.Providers[pID]
		if p.Protocol == ProtocolAnthropicMessages {
			path := filepath.Join(home, ".claude", "settings.json")
			existing, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return nil, nil, err
			}
			root, err := jsonMap(existing)
			if err != nil {
				return nil, nil, err
			}
			env := subMap(root, "env")
			env["ANTHROPIC_BASE_URL"] = strings.TrimSuffix(p.BaseURL, "/v1")
			if p.APIKeyEnv != "" {
				env["ANTHROPIC_API_KEY"] = "${" + p.APIKeyEnv + "}"
			} else if p.APIKey != "" {
				env["ANTHROPIC_API_KEY"] = p.APIKey
			}
			content, err := encodeJSON(root)
			if err != nil {
				return nil, nil, err
			}
			files = append(files, OutFile{Path: path, Content: content})
		} else {
			notes = append(notes, fmt.Sprintf("provider %q is %s — Claude Code needs the anthropic-messages protocol; settings.json left untouched", pID, p.Protocol))
		}
	}

	mcpPath := filepath.Join(home, ".claude", "mcp.json")
	existing, err := os.ReadFile(mcpPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	root, err := jsonMap(existing)
	if err != nil {
		return nil, nil, err
	}
	servers := subMap(root, "mcpServers")
	for _, name := range c.sortedMCPServerNames() {
		s := c.MCPServers[name]
		if !s.IsEnabled() {
			continue
		}
		switch s.Transport {
		case TransportStdio:
			entry := map[string]any{"type": "stdio", "command": s.Command, "args": s.Args}
			if len(s.Env) > 0 {
				entry["env"] = s.Env
			}
			servers[name] = entry
		case TransportHTTP:
			entry := map[string]any{"type": "http", "url": s.URL}
			if len(s.Headers) > 0 {
				entry["headers"] = s.Headers
			}
			servers[name] = entry
		case TransportSSE:
			entry := map[string]any{"type": "sse", "url": s.URL}
			if len(s.Headers) > 0 {
				entry["headers"] = s.Headers
			}
			servers[name] = entry
		}
	}
	mcpJSON, err := encodeJSON(root)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, OutFile{Path: mcpPath, Content: mcpJSON})
	return files, notes, nil
}

// --- Codex (~/.codex/config.toml) ---

func (c *Config) renderCodex(home string) ([]OutFile, []string, error) {
	var notes []string
	path := filepath.Join(home, ".codex", "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	root := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := toml.Unmarshal(existing, &root); err != nil {
			return nil, nil, fmt.Errorf("parse existing %s (comments are not preserved): %w", path, err)
		}
	}

	if c.Defaults.Model != "" {
		root["model"] = c.defaultModelID()
		root["model_provider"] = c.defaultProviderID()
	}
	modelProviders := subMap(root, "model_providers")
	for _, id := range c.orderedProviderIDs() {
		p := c.Providers[id]
		wireAPI := "chat"
		if p.Protocol == ProtocolResponses {
			wireAPI = "responses"
		}
		envKey := p.APIKeyEnv
		if envKey == "" {
			envKey = strings.ToUpper(id) + "_API_KEY"
			notes = append(notes, fmt.Sprintf("provider %q has a literal key — export %s before using codex", id, envKey))
		}
		modelProviders[id] = map[string]any{
			"name":     p.Name,
			"base_url": p.BaseURL,
			"env_key":  envKey,
			"wire_api": wireAPI,
		}
	}

	mcpServers := subMap(root, "mcp_servers")
	for _, name := range c.sortedMCPServerNames() {
		s := c.MCPServers[name]
		if !s.IsEnabled() {
			continue
		}
		if s.Transport != TransportStdio {
			notes = append(notes, fmt.Sprintf("mcp server %q skipped (%s transport not supported here)", name, s.Transport))
			continue
		}
		entry := map[string]any{"command": s.Command, "args": s.Args}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		mcpServers[name] = entry
	}

	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return nil, nil, err
	}
	return []OutFile{{Path: path, Content: []byte(buf.String())}}, notes, nil
}

// sortedMCPServerNames returns MCP server names in stable order.
func (c *Config) sortedMCPServerNames() []string {
	names := make([]string, 0, len(c.MCPServers))
	for name := range c.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WriteFiles writes rendered files, backing up any existing file to
// "<path>.bak" first.
func WriteFiles(files []OutFile) ([]string, error) {
	var written []string
	for _, f := range files {
		if data, err := os.ReadFile(f.Path); err == nil {
			if err := os.WriteFile(f.Path+".bak", data, 0o644); err != nil {
				return written, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return written, err
		}
		written = append(written, f.Path)
	}
	return written, nil
}
