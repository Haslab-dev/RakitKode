package agentconfig

import (
	"os"
)

// Merge overlays override onto base, entity by entity:
//   - Providers keyed by ID
//   - Models keyed by "provider/id"
//   - MCP servers keyed by name
//   - Defaults.Model replaced when the override sets one
//
// Entities present only in base are kept; an override entity replaces the
// base entity wholesale. To turn a base entity off for one app, copy it into
// the override with enabled=false.
func Merge(base, override *Config) *Config {
	if base == nil {
		base = &Config{}
	}
	out := Config{
		Version:    Version,
		Providers:  map[string]Provider{},
		Models:     map[string]Model{},
		MCPServers: map[string]MCPServer{},
		Defaults:   base.Defaults,
	}
	for id, p := range base.Providers {
		out.Providers[id] = p
	}
	for full, m := range base.Models {
		out.Models[full] = m
	}
	for name, s := range base.MCPServers {
		out.MCPServers[name] = s
	}
	if override == nil {
		return &out
	}
	for id, p := range override.Providers {
		out.Providers[id] = p
	}
	for full, m := range override.Models {
		out.Models[full] = m
	}
	for name, s := range override.MCPServers {
		out.MCPServers[name] = s
	}
	if override.Defaults.Model != "" {
		out.Defaults.Model = override.Defaults.Model
	}
	return &out
}

// LoadLayered loads canonical configs in priority order — first path is the
// base, each later path overrides it. Missing files are skipped, so hosts can
// unconditionally pass [globalCanonical, appOverride]. Returns nil when no
// file exists at all.
func LoadLayered(paths ...string) (*Config, error) {
	var out *Config
	for _, p := range paths {
		if p == "" {
			continue
		}
		c, err := Load(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		out = Merge(out, c)
	}
	return out, nil
}
