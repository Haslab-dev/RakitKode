package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Haslab-dev/RakitKode/events"
)

func TestMarketplaceInstallUninstall(t *testing.T) {
	dataDir := t.TempDir()
	bus := events.NewBus()
	m := NewManager(dataDir, bus)

	// A local plugin directory plus a marketplace manifest pointing at it.
	pluginSrc := filepath.Join(t.TempDir(), "demo-plugin")
	if err := os.MkdirAll(pluginSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Plugin{
		ID:          "demo-plugin",
		Name:        "Demo Plugin",
		Description: "A demo",
		Version:     "1.0.0",
		Enabled:     true,
		Tools: []PluginToolDef{{
			Name:        "demo_tool",
			Description: "does demo things",
			HandlerType: HandlerCommand,
			Command:     "echo demo",
		}},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(pluginSrc, "plugin.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	marketplacePath := filepath.Join(t.TempDir(), "marketplace.json")
	mp := MarketplaceManifest{
		Name: "test-marketplace",
		Plugins: []MarketplacePlugin{{
			ID: "demo-plugin", Name: "Demo Plugin", Version: "1.0.0", Path: pluginDirRel(pluginSrc),
		}},
	}
	// Use an absolute path so the source resolves regardless of manifest dir.
	mp.Plugins[0].Path = pluginSrc
	data, _ = json.MarshalIndent(mp, "", "  ")
	if err := os.WriteFile(marketplacePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.AddMarketplace("test", MarketplaceSource{Source: "file", Path: marketplacePath}); err != nil {
		t.Fatal(err)
	}
	if got := len(m.ListMarketplaces()); got != 1 {
		t.Fatalf("marketplaces = %d", got)
	}

	installed, err := m.InstallPlugin("test", "demo-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if installed.ID != "demo-plugin" || installed.Name != "Demo Plugin" {
		t.Fatalf("installed = %+v", installed)
	}
	if _, ok := m.Get("demo-plugin"); !ok {
		t.Fatal("installed plugin not registered")
	}
	if got := len(m.InstalledPlugins()); got != 1 {
		t.Fatalf("installed records = %d", got)
	}

	if err := m.UninstallPlugin("demo-plugin"); err != nil {
		t.Fatal(err)
	}
	if got := len(m.InstalledPlugins()); got != 0 {
		t.Fatalf("records after uninstall = %d", got)
	}
}

func pluginDirRel(abs string) string {
	// Kept explicit for readability in the manifest fixture.
	if filepath.IsAbs(abs) {
		return abs
	}
	return abs
}
