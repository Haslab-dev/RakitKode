package agentconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
)

// ImportFromEngine seeds a canonical Config from the rakit engine home:
// providers_config.json (profiles + active model) and mcp_servers.json.
// Entries already present in base (a previously loaded canonical config) are
// overwritten by engine values under the same key; unknown keys in base are
// preserved.
func ImportFromEngine(dataDir string, base *Config) (*Config, error) {
	if base == nil {
		base = &Config{
			Providers:  map[string]Provider{},
			Models:     map[string]Model{},
			MCPServers: map[string]MCPServer{},
		}
	}

	profilesData, err := os.ReadFile(filepath.Join(dataDir, "providers_config.json"))
	if err != nil {
		return nil, fmt.Errorf("read providers_config.json: %w", err)
	}
	var profiles []llm.ProviderProfile
	if err := json.Unmarshal(profilesData, &profiles); err != nil {
		return nil, fmt.Errorf("parse providers_config.json: %w", err)
	}

	var active llm.Profile
	if activeData, err := os.ReadFile(filepath.Join(dataDir, "profiles.json")); err == nil {
		_ = json.Unmarshal(activeData, &active)
	}

	for _, p := range profiles {
		if !p.Enabled || p.APIKey == "" {
			continue
		}
		id := normalizeID(p.ID)
		base.Providers[id] = Provider{
			Name:     p.Name,
			Type:     "openai-compatible",
			BaseURL:  p.BaseURL,
			APIKey:   p.APIKey,
			Protocol: ProtocolChatCompletions,
		}
		models := p.SelectedModels
		if len(models) == 0 {
			models = p.AvailableModels
		}
		for _, m := range models {
			full := id + "/" + m
			if _, exists := base.Models[full]; exists {
				continue
			}
			base.Models[full] = Model{Provider: id, ID: m}
		}
	}

	serversData, err := os.ReadFile(filepath.Join(dataDir, "mcp_servers.json"))
	if err == nil {
		var servers []mcp.ServerConfig
		if err := json.Unmarshal(serversData, &servers); err == nil {
			for _, s := range servers {
				transport := TransportStdio
				if s.Type == "remote" && s.URL != "" {
					transport = TransportHTTP
				}
				enabled := s.Enabled
				base.MCPServers[normalizeID(s.Name)] = MCPServer{
					Transport: transport,
					Command:   s.Command,
					Args:      s.Args,
					Env:       s.Env,
					URL:       s.URL,
					Enabled:   &enabled,
				}
			}
		}
	}

	if active.ProviderID != "" && active.Model != "" {
		base.Defaults.Model = normalizeID(active.ProviderID) + "/" + active.Model
	}
	if err := base.Validate(); err != nil {
		return nil, err
	}
	return base, nil
}

// ImportMCPRow merges one server row (host-database independent) into the
// canonical config. env entries shaped "header:<Name>" become HTTP headers. (host database independent) into the
// canonical config. env entries shaped "header:<Name>" become headers.
func ImportMCPRow(base *Config, id, transport, url, envJSON string, enabled bool) error {
	if base.MCPServers == nil {
		base.MCPServers = map[string]MCPServer{}
	}
	s := MCPServer{
		Transport: strings.ToLower(transport),
		URL:       url,
		Enabled:   &enabled,
	}
	if s.Transport == "" {
		s.Transport = TransportHTTP
	}
	if envJSON != "" {
		var raw map[string]string
		if err := json.Unmarshal([]byte(envJSON), &raw); err == nil {
			for k, v := range raw {
				if header, ok := strings.CutPrefix(k, "header:"); ok {
					if s.Headers == nil {
						s.Headers = map[string]string{}
					}
					s.Headers[header] = v
					continue
				}
				if s.Env == nil {
					s.Env = map[string]string{}
				}
				s.Env[k] = v
			}
		}
	}
	base.MCPServers[normalizeID(id)] = s
	return nil
}

// normalizeID lowercases and slugifies identifiers (spaces → dashes).
func normalizeID(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "-"))
}
