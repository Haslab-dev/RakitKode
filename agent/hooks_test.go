package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Haslab-dev/RakitKode/hooks"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
	"github.com/Haslab-dev/RakitKode/plugins"
	"github.com/Haslab-dev/RakitKode/search"
	"github.com/Haslab-dev/RakitKode/skills"
	"github.com/Haslab-dev/RakitKode/tools"
)

// hookMarker builds a hook command that appends a marker line to file.
func hookMarker(file, marker string) hooks.Command {
	return hooks.Command{
		Type:    "command",
		Command: "printf '%s\\n' " + marker + " >> " + file,
		Timeout: 10,
	}
}

func waitForMarker(t *testing.T, file, marker string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(file)
		if err == nil && strings.Contains(string(data), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(file)
	t.Fatalf("marker %q not observed in %s within 10s; content: %q", marker, file, string(data))
}

func newHookTestManager(t *testing.T, ws string, cfg hooks.Config) *Manager {
	t.Helper()
	tempDir := t.TempDir()
	mgr := NewManagerWithOptions(ManagerOptions{
		LLMClient:    llm.NewLLMClient(tempDir),
		ToolRegistry: tools.NewRegistry(search.NewSearchManager()),
		Skills:       skills.NewManager(""),
		Plugins:      plugins.NewManager(tempDir, nil),
		MCP:          mcp.NewManager(tempDir),
		DataDir:      tempDir,
		Workspace:    ws,
		Hooks:        hooks.NewRunner(cfg, ws),
	})
	return mgr
}

func TestSessionStartHookFiresOnCreateAndLoad(t *testing.T) {
	ws := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "markers.log")
	cfg := hooks.Config{
		"SessionStart": []hooks.Matcher{
			{Hooks: []hooks.Command{hookMarker(markerFile, "start-create")}},
		},
	}
	mgr := newHookTestManager(t, ws, cfg)

	sess, err := mgr.CreateSession("Hooked", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitForMarker(t, markerFile, "start-create")

	// loadSessions (restore path): a fresh manager over the same store fires
	// SessionStart with source=load. Use a second marker file to prove the
	// restored (not re-created) session fires it.
	marker2 := filepath.Join(t.TempDir(), "markers2.log")
	cfg2 := hooks.Config{
		"SessionStart": []hooks.Matcher{
			{Hooks: []hooks.Command{hookMarker(marker2, "start-load")}},
		},
	}
	_ = sess
	dataDir := filepath.Dir(mgr.storePath)
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	mgr2 := NewManagerWithOptions(ManagerOptions{
		LLMClient:    llm.NewLLMClient(t.TempDir()),
		ToolRegistry: tools.NewRegistry(search.NewSearchManager()),
		Skills:       skills.NewManager(""),
		Plugins:      plugins.NewManager(t.TempDir(), nil),
		MCP:          mcp.NewManager(t.TempDir()),
		DataDir:      dataDir,
		Hooks:        hooks.NewRunner(cfg2, ws),
		Workspace:    ws,
	})
	if got := len(mgr2.ListSessions()); got == 0 {
		t.Fatal("expected restored sessions from the shared store")
	}
	waitForMarker(t, marker2, "start-load")
}

func TestSessionEndHookFiresOnDelete(t *testing.T) {
	ws := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "end.log")
	cfg := hooks.Config{
		"SessionEnd": []hooks.Matcher{
			{Hooks: []hooks.Command{hookMarker(markerFile, "session-end")}},
		},
	}
	mgr := newHookTestManager(t, ws, cfg)
	sess, err := mgr.CreateSession("Doomed", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	mgr.DeleteSession(sess.ID)
	waitForMarker(t, markerFile, "session-end")
}

func TestPreCompactHookBlocksAndPasses(t *testing.T) {
	ws := t.TempDir()

	// 1. Blocking hook (exit 2) must cancel compaction.
	blockFile := filepath.Join(t.TempDir(), "pre.log")
	blockCfg := hooks.Config{
		"PreCompact": []hooks.Matcher{
			{Hooks: []hooks.Command{{Type: "command", Command: "exit 2", Timeout: 10}}},
			{Hooks: []hooks.Command{hookMarker(blockFile, "should-not-run")}},
		},
	}
	mgr := newHookTestManager(t, ws, blockCfg)
	sess, err := mgr.CreateSession("Compact me", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < 10; i++ {
		sess.Messages = append(sess.Messages, AgentMessage{
			ID: string(rune('a' + i)), Role: "user",
			Content:   []ContentBlock{{Type: "text", Text: "filler message"}},
			Timestamp: time.Now(),
		})
	}
	mgr.mu.Lock()
	mgr.sessions[sess.ID] = sess
	mgr.mu.Unlock()
	if _, err := mgr.CompactSession(sess.ID); err == nil {
		t.Fatal("expected compaction to be blocked by hook")
	}
	if data, _ := os.ReadFile(blockFile); strings.Contains(string(data), "should-not-run") {
		t.Fatal("hooks after the blocking hook must not run")
	}

	// 2. Passing hook runs before compaction and lets it proceed.
	passFile := filepath.Join(t.TempDir(), "pre-ok.log")
	passCfg := hooks.Config{
		"PreCompact": []hooks.Matcher{
			{Hooks: []hooks.Command{hookMarker(passFile, "pre-compact")}},
		},
	}
	mgr2 := newHookTestManager(t, ws, passCfg)
	sess2, err := mgr2.CreateSession("Compact me too", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < 10; i++ {
		sess2.Messages = append(sess2.Messages, AgentMessage{
			ID: string(rune('a' + i)), Role: "user",
			Content:   []ContentBlock{{Type: "text", Text: "filler message"}},
			Timestamp: time.Now(),
		})
	}
	mgr2.mu.Lock()
	mgr2.sessions[sess2.ID] = sess2
	mgr2.mu.Unlock()
	if _, err := mgr2.CompactSession(sess2.ID); err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	waitForMarker(t, passFile, "pre-compact")
	mgr2.mu.RLock()
	after := len(mgr2.sessions[sess2.ID].Messages)
	mgr2.mu.RUnlock()
	if after >= 10 {
		t.Fatalf("compaction should reduce messages, still %d", after)
	}
}

func TestStopHookStillFiresOnFinishTurn(t *testing.T) {
	ws := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "stop.log")
	cfg := hooks.Config{
		"Stop": []hooks.Matcher{
			{Hooks: []hooks.Command{hookMarker(markerFile, "turn-stop")}},
		},
	}
	mgr := newHookTestManager(t, ws, cfg)
	sess, err := mgr.CreateSession("Stopper", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	mgr.mu.Lock()
	sess.State = StateThinking
	mgr.mu.Unlock()
	mgr.finishTurn(sess.ID, "")
	waitForMarker(t, markerFile, "turn-stop")
}
