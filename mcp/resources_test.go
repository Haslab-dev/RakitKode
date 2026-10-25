package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPResourcesAndPrompts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"serverInfo":   map[string]any{"name": "test"},
				"capabilities": map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}},
			}
		case "resources/list":
			result = map[string]any{"resources": []any{
				map[string]any{"uri": "file:///a.txt", "name": "a", "mimeType": "text/plain"},
			}}
		case "resources/read":
			result = map[string]any{"contents": []any{map[string]any{"text": "hello"}}}
		case "prompts/list":
			result = map[string]any{"prompts": []any{
				map[string]any{"name": "review", "description": "review a diff", "arguments": []any{
					map[string]any{"name": "target", "required": true},
				}},
			}}
		case "prompts/get":
			result = map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "review main.go"}},
			}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()

	mgr := NewManager(t.TempDir())
	if _, err := mgr.SaveServer(ServerConfig{Name: "remote", Type: "http", URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ConnectAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	resources := mgr.ListResources(context.Background())
	if len(resources) != 1 || resources[0].URI != "file:///a.txt" {
		t.Fatalf("resources = %+v", resources)
	}
	text, err := mgr.ReadResource(context.Background(), "remote", "file:///a.txt")
	if err != nil || text != "hello" {
		t.Fatalf("read = %q err=%v", text, err)
	}

	prompts := mgr.ListPrompts(context.Background())
	if len(prompts) != 1 || prompts[0].Name != "review" {
		t.Fatalf("prompts = %+v", prompts)
	}
	out, err := mgr.GetPrompt(context.Background(), "remote", "review", map[string]any{"target": "main.go"})
	if err != nil || out == "" {
		t.Fatalf("prompt = %q err=%v", out, err)
	}
}
