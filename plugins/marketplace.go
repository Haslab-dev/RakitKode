package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MarketplaceSource describes where a marketplace manifest lives.
type MarketplaceSource struct {
	Source string `json:"source"` // "file" or "url"
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
}

// KnownMarketplace is a registered marketplace.
type KnownMarketplace struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Source  MarketplaceSource `json:"source"`
	AddedAt time.Time         `json:"added_at"`
}

// MarketplacePlugin is one entry in a marketplace manifest.
type MarketplacePlugin struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Version      string           `json:"version"`
	Author       string           `json:"author,omitempty"`
	Path         string           `json:"path,omitempty"` // local plugin directory
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Tools        []PluginToolDef  `json:"tools,omitempty"`
	Skills       []PluginSkillDef `json:"skills,omitempty"`
}

// MarketplaceManifest is the marketplace.json shape.
type MarketplaceManifest struct {
	Name    string              `json:"name"`
	Version string              `json:"version,omitempty"`
	Plugins []MarketplacePlugin `json:"plugins"`
}

// InstalledPluginRecord tracks an installed marketplace plugin.
type InstalledPluginRecord struct {
	ID          string    `json:"id"`
	Marketplace string    `json:"marketplace"`
	Version     string    `json:"version,omitempty"`
	Dir         string    `json:"dir"`
	InstalledAt time.Time `json:"installed_at"`
}

func (m *Manager) storageRoot() string {
	if m.globalDir == "" {
		return ""
	}
	return filepath.Dir(m.globalDir)
}

func (m *Manager) knownMarketplacesPath() string {
	return filepath.Join(m.storageRoot(), "known_marketplaces.json")
}

func (m *Manager) installedPluginsPath() string {
	return filepath.Join(m.storageRoot(), "installed_plugins.json")
}

// ListMarketplaces returns registered marketplaces sorted by ID.
func (m *Manager) ListMarketplaces() []KnownMarketplace {
	var list []KnownMarketplace
	_ = readJSONFile(m.knownMarketplacesPath(), &list)
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// AddMarketplace registers a marketplace manifest (file path or URL).
func (m *Manager) AddMarketplace(name string, src MarketplaceSource) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("marketplace name is required")
	}
	if src.Source == "" {
		if src.Path != "" {
			src.Source = "file"
		} else if src.URL != "" {
			src.Source = "url"
		}
	}
	if src.Source == "file" && src.Path == "" {
		return fmt.Errorf("file marketplace requires a path")
	}
	if src.Source == "url" && src.URL == "" {
		return fmt.Errorf("url marketplace requires a url")
	}
	list := m.ListMarketplaces()
	for _, mp := range list {
		if mp.ID == name {
			return fmt.Errorf("marketplace %q already exists", name)
		}
	}
	list = append(list, KnownMarketplace{ID: name, Name: name, Source: src, AddedAt: time.Now()})
	return writeJSONFile(m.knownMarketplacesPath(), list)
}

// RemoveMarketplace unregisters a marketplace (installed plugins stay).
func (m *Manager) RemoveMarketplace(name string) error {
	list := m.ListMarketplaces()
	out := list[:0]
	found := false
	for _, mp := range list {
		if mp.ID == name {
			found = true
			continue
		}
		out = append(out, mp)
	}
	if !found {
		return fmt.Errorf("marketplace %q not found", name)
	}
	return writeJSONFile(m.knownMarketplacesPath(), out)
}

// FetchMarketplace loads and parses a marketplace manifest.
func (m *Manager) FetchMarketplace(name string) (*MarketplaceManifest, error) {
	var src MarketplaceSource
	found := false
	for _, mp := range m.ListMarketplaces() {
		if mp.ID == name {
			src = mp.Source
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("marketplace %q not found", name)
	}
	var data []byte
	switch src.Source {
	case "file", "":
		b, err := os.ReadFile(src.Path)
		if err != nil {
			return nil, err
		}
		data = b
	case "url":
		resp, err := http.Get(src.URL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("marketplace %s: status %d", name, resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported marketplace source %q", src.Source)
	}
	var manifest MarketplaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse marketplace %s: %w", name, err)
	}
	return &manifest, nil
}

// InstallPlugin installs a plugin from a marketplace into the global plugins
// directory and registers it immediately.
func (m *Manager) InstallPlugin(marketplaceName, pluginID string) (*Plugin, error) {
	manifest, err := m.FetchMarketplace(marketplaceName)
	if err != nil {
		return nil, err
	}
	var entry *MarketplacePlugin
	for i := range manifest.Plugins {
		if manifest.Plugins[i].ID == pluginID {
			entry = &manifest.Plugins[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("plugin %q not found in marketplace %q", pluginID, marketplaceName)
	}

	dest := filepath.Join(m.globalDir, entry.ID)
	_ = os.RemoveAll(dest)
	if entry.Path != "" {
		src := entry.Path
		if !filepath.IsAbs(src) {
			// Relative paths resolve against the manifest directory for file
			// marketplaces.
			for _, mp := range m.ListMarketplaces() {
				if mp.ID == marketplaceName && mp.Source.Source == "file" {
					src = filepath.Join(filepath.Dir(mp.Source.Path), src)
					break
				}
			}
		}
		if err := copyDir(src, dest); err != nil {
			return nil, fmt.Errorf("copy plugin files: %w", err)
		}
	} else {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return nil, err
		}
		p := Plugin{
			ID:           entry.ID,
			Name:         entry.Name,
			Description:  entry.Description,
			Version:      entry.Version,
			Author:       entry.Author,
			Enabled:      true,
			Source:       SourceGlobal,
			SystemPrompt: entry.SystemPrompt,
			Tools:        entry.Tools,
			Skills:       entry.Skills,
		}
		if err := writeJSONFile(filepath.Join(dest, "plugin.json"), p); err != nil {
			return nil, err
		}
	}

	records := m.installedPlugins()
	records = append(records, InstalledPluginRecord{
		ID:          entry.ID,
		Marketplace: marketplaceName,
		Version:     entry.Version,
		Dir:         dest,
		InstalledAt: time.Now(),
	})
	if err := writeJSONFile(m.installedPluginsPath(), records); err != nil {
		return nil, err
	}
	m.Reload()
	installed, _ := m.Get(entry.ID)
	if installed == nil {
		return nil, fmt.Errorf("plugin %s installed but not discovered", entry.ID)
	}
	return installed, nil
}

// UninstallPlugin removes an installed marketplace plugin.
func (m *Manager) UninstallPlugin(pluginID string) error {
	records := m.installedPlugins()
	out := records[:0]
	var dir string
	for _, r := range records {
		if r.ID == pluginID {
			dir = r.Dir
			continue
		}
		out = append(out, r)
	}
	if dir == "" {
		return fmt.Errorf("plugin %q is not installed from a marketplace", pluginID)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := writeJSONFile(m.installedPluginsPath(), out); err != nil {
		return err
	}
	m.Reload()
	return nil
}

// InstalledPlugins lists marketplace-installed plugins.
func (m *Manager) InstalledPlugins() []InstalledPluginRecord {
	return m.installedPlugins()
}

func (m *Manager) installedPlugins() []InstalledPluginRecord {
	var list []InstalledPluginRecord
	_ = readJSONFile(m.installedPluginsPath(), &list)
	return list
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
