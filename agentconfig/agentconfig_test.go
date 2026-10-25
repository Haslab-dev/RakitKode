package agentconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

func fixtureConfig() *Config {
	enabled := true
	return &Config{
		Providers: map[string]Provider{
			"kenari": {Name: "Kenari", Type: "openai-compatible", BaseURL: "https://kenari.id/v1", APIKey: "sk-test", Protocol: ProtocolChatCompletions},
		},
		Models: map[string]Model{
			"kenari/glm-5-3-flash":       {Provider: "kenari", ID: "glm-5-3-flash", Name: "GLM Flash"},
			"kenari/gpt-6-luna":          {Provider: "kenari", ID: "gpt-6-luna"},
			"kenari/deepseek-v4-1-flash": {Provider: "kenari", ID: "deepseek-v4-1-flash", Reasoning: true},
		},
		MCPServers: map[string]MCPServer{
			"firecrawl": {Transport: TransportHTTP, URL: "https://mcp.firecrawl.dev/v2/mcp",
				Headers: map[string]string{"Authorization": "Bearer fc-test"}, Enabled: &enabled},
			"computer-use": {Transport: TransportStdio, Command: "node", Args: []string{"server.js"},
				Env: map[string]string{"DEBUG": "1"}, Enabled: &enabled},
		},
		Defaults: Defaults{Model: "kenari/glm-5-3-flash"},
	}
}

// existingNative mimics a user's native file with an agent-specific server
// that adapters must preserve.
const existingOpenCode = `{
  "mcp": {
    "pencil": { "type": "local", "command": ["/Apps/Pen.app/mcp"], "enabled": true }
  },
  "theme": "dark"
}`

func TestRenderOpenCodeMergesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(existingOpenCode), 0o644); err != nil {
		t.Fatal(err)
	}

	files, _, err := fixtureConfig().Render(TargetOpenCode, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != path {
		t.Fatalf("unexpected files: %+v", files)
	}
	var view map[string]any
	if err := json.Unmarshal(files[0].Content, &view); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if view["theme"] != "dark" {
		t.Fatal("unmanaged key theme lost")
	}
	mcp := view["mcp"].(map[string]any)
	if _, ok := mcp["pencil"]; !ok {
		t.Fatal("existing pencil server lost")
	}
	if _, ok := mcp["firecrawl"]; !ok {
		t.Fatal("canonical firecrawl server missing")
	}
	if mcp["firecrawl"].(map[string]any)["type"] != "remote" {
		t.Fatal("http server must render as remote")
	}
	prov := view["provider"].(map[string]any)["kenari"].(map[string]any)
	if prov["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("provider npm = %v", prov["npm"])
	}
	if view["model"] != "kenari/glm-5-3-flash" {
		t.Fatalf("model = %v", view["model"])
	}
}

func TestRenderPiShapes(t *testing.T) {
	files, notes, err := fixtureConfig().Render(TargetPi, "/home")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("pi renders %d files, want 2", len(files))
	}
	var sawModels, sawMCP bool
	var httpSkipped bool
	for _, f := range files {
		switch {
		case strings.HasSuffix(f.Path, "models.json"):
			sawModels = true
			var doc struct {
				Providers map[string]struct {
					BaseURL string `yaml:"baseUrl" json:"baseUrl"`
					APIKey  string `yaml:"apiKey" json:"apiKey"`
					Models  []struct {
						ID string `json:"id"`
					} `json:"models"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(f.Content, &doc); err != nil {
				t.Fatal(err)
			}
			p := doc.Providers["kenari"]
			if p.BaseURL != "https://kenari.id/v1" || p.APIKey != "sk-test" {
				t.Fatalf("provider shape: %+v", p)
			}
			if len(p.Models) != 3 {
				t.Fatalf("models = %d, want 3", len(p.Models))
			}
		case strings.HasSuffix(f.Path, "mcp.json"):
			sawMCP = true
			var doc struct {
				MCPServers map[string]map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal(f.Content, &doc); err != nil {
				t.Fatal(err)
			}
			if _, ok := doc.MCPServers["computer-use"]; !ok {
				t.Fatal("stdio server missing")
			}
			if _, ok := doc.MCPServers["firecrawl"]; ok {
				t.Fatal("http server must be skipped for pi")
			}
			httpSkipped = true
		}
	}
	if !sawModels || !sawMCP || !httpSkipped {
		t.Fatalf("pi files incomplete: models=%v mcp=%v httpSkipped=%v notes=%v", sawModels, sawMCP, httpSkipped, notes)
	}
}

func TestRenderOMPYAMLAndMCPSkip(t *testing.T) {
	files, _, err := fixtureConfig().Render(TargetOMP, "/home")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Providers map[string]struct {
			BaseURL string `yaml:"baseUrl"`
			APIKey  string `yaml:"apiKey"`
		} `yaml:"providers"`
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, "models.yml") {
			if err := yaml.Unmarshal(f.Content, &doc); err != nil {
				t.Fatal(err)
			}
		}
	}
	if doc.Providers["kenari"].BaseURL != "https://kenari.id/v1" {
		t.Fatalf("omp yaml providers: %+v", doc.Providers)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, "mcp.json") {
			if strings.Contains(string(f.Content), "firecrawl") {
				t.Fatal("http server must be skipped for omp")
			}
		}
	}
}

func TestRenderClaudeSkipsOpenAIProvider(t *testing.T) {
	files, notes, err := fixtureConfig().Render(TargetClaude, "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, "settings.json") {
			t.Fatal("settings.json must not be emitted without an anthropic-messages provider")
		}
		if strings.HasSuffix(f.Path, "mcp.json") {
			var doc struct {
				MCPServers map[string]map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal(f.Content, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.MCPServers["firecrawl"]["type"] != "http" {
				t.Fatalf("firecrawl type = %v", doc.MCPServers["firecrawl"]["type"])
			}
			if doc.MCPServers["firecrawl"]["headers"] == nil {
				t.Fatal("headers lost")
			}
		}
	}
	if len(notes) == 0 {
		t.Fatal("expected a protocol note")
	}
}

func TestRenderCodexMergesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `
[mcp_servers.pencil]
command = "/Apps/Pen.app/mcp"
args = ["--app", "desktop"]
`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes, err := fixtureConfig().Render(TargetCodex, dir)
	_ = notes
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Path != path {
		t.Fatalf("codex path = %s", files[0].Path)
	}
	var doc map[string]any
	if err := toml.Unmarshal(files[0].Content, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "glm-5-3-flash" || doc["model_provider"] != "kenari" {
		t.Fatalf("defaults: %v %v", doc["model"], doc["model_provider"])
	}
	providers := doc["model_providers"].(map[string]any)["kenari"].(map[string]any)
	if providers["env_key"] != "KENARI_API_KEY" {
		t.Fatalf("env_key = %v", providers["env_key"])
	}
	if providers["wire_api"] != "chat" {
		t.Fatalf("wire_api = %v", providers["wire_api"])
	}
	servers := doc["mcp_servers"].(map[string]any)
	if _, ok := servers["pencil"]; !ok {
		t.Fatal("existing pencil server lost")
	}
	if _, ok := servers["firecrawl"]; ok {
		t.Fatal("http server must be skipped for codex")
	}
}

func TestImportFromEngineAndValidate(t *testing.T) {
	dir := t.TempDir()
	providers := []map[string]any{{
		"id": "prov-1", "name": "Kenari", "api_key": "sk-live", "base_url": "https://kenari.id/v1",
		"enabled": true, "selected_models": []string{"glm-5-3-flash", "gpt-6-luna"},
	}}
	data, _ := json.Marshal(providers)
	if err := os.WriteFile(filepath.Join(dir, "providers_config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(`{"provider_id":"prov-1","model":"glm-5-3-flash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers := []map[string]any{{"name": "computer-use", "command": "node", "args": []string{"s.js"}, "enabled": false}}
	sdata, _ := json.Marshal(servers)
	if err := os.WriteFile(filepath.Join(dir, "mcp_servers.json"), sdata, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := ImportFromEngine(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["prov-1"].APIKey != "sk-live" {
		t.Fatal("provider key not imported")
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("models = %d", len(cfg.Models))
	}
	if cfg.Defaults.Model != "prov-1/glm-5-3-flash" {
		t.Fatalf("defaults = %q", cfg.Defaults.Model)
	}
	if cfg.MCPServers["computer-use"].IsEnabled() {
		t.Fatal("disabled server must stay disabled")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestImportMCPRowHeaderMapping(t *testing.T) {
	cfg := &Config{}
	if err := ImportMCPRow(cfg, "firecrawl", "http", "https://mcp.firecrawl.dev/v2/mcp",
		`{"header:Authorization":"Bearer fc-live"}`, true); err != nil {
		t.Fatal(err)
	}
	s := cfg.MCPServers["firecrawl"]
	if s.Headers["Authorization"] != "Bearer fc-live" {
		t.Fatalf("headers = %+v", s.Headers)
	}
	if s.Transport != TransportHTTP {
		t.Fatalf("transport = %q", s.Transport)
	}
}
