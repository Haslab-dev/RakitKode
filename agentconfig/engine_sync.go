package agentconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
)

// EngineSyncOptions controls how SyncToEngineDir writes the engine's
// derived configuration files.
type EngineSyncOptions struct {
	// SetActiveModel writes profiles.json (the engine-wide active model)
	// from Defaults.Model. When false an existing profiles.json is left
	// alone — apps keep their own model choice unless they opt in.
	SetActiveModel bool
}

// SyncToEngineDir upserts the canonical providers/models/MCP servers into
// the engine's derived configuration files in dir:
//
//	providers_config.json  ([]llm.ProviderProfile)
//	profiles.json          (active model; see EngineSyncOptions)
//	mcp_servers.json       ([]mcp.ServerConfig)
//
// It is a merge, not a replace: engine-side entries the canonical config
// does not know (placeholder providers, app-added stdio servers) are kept.
// HTTP/SSE MCP servers are skipped — the engine's MCP manager currently
// speaks stdio only — and reported in the returned notes.
func SyncToEngineDir(cfg *Config, dir string, opts EngineSyncOptions) ([]string, []string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	var written, notes []string

	// providers_config.json — upsert canonical providers, keep unknown ones.
	profilesPath := filepath.Join(dir, "providers_config.json")
	var profiles []llm.ProviderProfile
	if data, err := os.ReadFile(profilesPath); err == nil {
		_ = json.Unmarshal(data, &profiles)
	}
	index := map[string]int{}
	for i, p := range profiles {
		index[p.ID] = i
	}
	for _, id := range cfg.orderedProviderIDs() {
		p := cfg.Providers[id]
		models := []string{}
		for _, m := range cfg.modelsOf(id) {
			models = append(models, m.ID)
		}
		entry := llm.ProviderProfile{
			ID:              id,
			Name:            p.Name,
			APIKey:          p.APIKey,
			BaseURL:         p.BaseURL,
			Enabled:         true,
			AvailableModels: models,
			SelectedModels:  models,
		}
		if p.APIKeyEnv != "" {
			// Keep any engine-side literal key; the env reference wins only
			// when the engine entry has none.
			if i, ok := index[id]; ok && profiles[i].APIKey == "" {
				entry.APIKey = os.Getenv(p.APIKeyEnv)
			}
		}
		if i, ok := index[id]; ok {
			profiles[i] = entry
		} else {
			profiles = append(profiles, entry)
			index[id] = len(profiles) - 1
		}
	}
	if data, err := json.MarshalIndent(profiles, "", "  "); err != nil {
		return written, notes, err
	} else if err := os.WriteFile(profilesPath, append(data, '\n'), 0o600); err != nil {
		return written, notes, err
	}
	written = append(written, profilesPath)

	// profiles.json — active model: written when the host opts in, or as a
	// first-run bootstrap when the engine has none yet. Existing choices are
	// never overwritten without the opt-in.
	activePath := filepath.Join(dir, "profiles.json")
	_, statErr := os.Stat(activePath)
	bootstrap := statErr != nil && os.IsNotExist(statErr)
	if (opts.SetActiveModel || bootstrap) && cfg.Defaults.Model != "" {
		pID, model := splitFullModel(cfg.Defaults.Model)
		p, ok := cfg.Providers[pID]
		if !ok {
			return written, notes, fmt.Errorf("defaults.model references unknown provider %q", pID)
		}
		active := llm.Profile{ProviderID: pID, APIKey: p.APIKey, BaseURL: p.BaseURL, Model: model}
		if data, err := json.MarshalIndent(active, "", "  "); err != nil {
			return written, notes, err
		} else if err := os.WriteFile(activePath, append(data, '\n'), 0o600); err != nil {
			return written, notes, err
		}
		written = append(written, activePath)
	}

	// mcp_servers.json — stdio servers only; engine speaks stdio today.
	mcpPath := filepath.Join(dir, "mcp_servers.json")
	var servers []mcp.ServerConfig
	if data, err := os.ReadFile(mcpPath); err == nil {
		_ = json.Unmarshal(data, &servers)
	}
	sindex := map[string]int{}
	for i, s := range servers {
		sindex[s.Name] = i
	}
	for _, name := range cfg.sortedMCPServerNames() {
		s := cfg.MCPServers[name]
		if s.Transport != TransportStdio {
			notes = append(notes, fmt.Sprintf("mcp server %q skipped (engine speaks stdio only; %s stays in the canonical config)", name, s.Transport))
			continue
		}
		entry := mcp.ServerConfig{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			Enabled: s.IsEnabled(),
		}
		if i, ok := sindex[name]; ok {
			entry.Status = servers[i].Status
			entry.LastError = servers[i].LastError
			entry.ToolCount = servers[i].ToolCount
			servers[i] = entry
		} else {
			servers = append(servers, entry)
			sindex[name] = len(servers) - 1
		}
	}
	if data, err := json.MarshalIndent(servers, "", "  "); err != nil {
		return written, notes, err
	} else if err := os.WriteFile(mcpPath, append(data, '\n'), 0o600); err != nil {
		return written, notes, err
	}
	written = append(written, mcpPath)

	return written, notes, nil
}

func splitFullModel(full string) (provider, model string) {
	for i := 0; i < len(full); i++ {
		if full[i] == '/' {
			return full[:i], full[i+1:]
		}
	}
	return "", full
}

// UpsertProviderProfiles merges engine-side provider profile edits (e.g. a
// host's provider settings UI) back into the canonical config: entity-level
// upsert by provider ID, last write wins per entity. Does not touch
// Defaults — model choice is per-app.
func UpsertProviderProfiles(canonicalPath string, profiles []llm.ProviderProfile) error {
	base, err := Load(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			base = &Config{
				Providers:  map[string]Provider{},
				Models:     map[string]Model{},
				MCPServers: map[string]MCPServer{},
			}
		} else {
			return err
		}
	}
	for _, p := range profiles {
		if !p.Enabled {
			continue
		}
		id := normalizeID(p.ID)
		models := p.SelectedModels
		if len(models) == 0 {
			models = p.AvailableModels
		}
		base.Providers[id] = Provider{
			Name:     p.Name,
			Type:     "openai-compatible",
			BaseURL:  p.BaseURL,
			APIKey:   p.APIKey,
			Protocol: ProtocolChatCompletions,
		}
		for _, m := range models {
			base.Models[id+"/"+m] = Model{Provider: id, ID: m}
		}
	}
	return Save(canonicalPath, base)
}
