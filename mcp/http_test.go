package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTransportConnectListAndCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			result = map[string]any{
				"serverInfo":   map[string]any{"name": "test"},
				"capabilities": map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "sess-1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			result = map[string]any{"tools": []any{map[string]any{
				"name":        "echo",
				"description": "echo text",
				"inputSchema": map[string]any{"type": "object"},
				"annotations": map[string]any{
					"title":        "Echo",
					"readOnlyHint": true,
				},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "hi"}}}
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
	tools := mgr.ListConnectedTools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if tools[0].Name != "remote/echo" || !tools[0].ReadOnly || tools[0].Title != "Echo" {
		t.Fatalf("unexpected tool: %+v", tools[0])
	}
	out, err := mgr.CallTool(context.Background(), "remote/echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi" {
		t.Fatalf("call output = %q", out)
	}
	mgr.DisconnectAll()
}
