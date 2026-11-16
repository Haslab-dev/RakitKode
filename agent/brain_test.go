package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Haslab-dev/RakitKode/brain"
	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/mcp"
	"github.com/Haslab-dev/RakitKode/plugins"
	"github.com/Haslab-dev/RakitKode/search"
	"github.com/Haslab-dev/RakitKode/skills"
	"github.com/Haslab-dev/RakitKode/store"
	"github.com/Haslab-dev/RakitKode/tools"
)

func newBrainTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	tempDir := t.TempDir()
	store.SetRoot(filepath.Join(tempDir, "rakit-home"))
	bus := events.NewBus()
	mgr := NewManagerWithOptions(ManagerOptions{
		LLMClient:    llm.NewLLMClient(tempDir),
		ToolRegistry: tools.NewRegistry(search.NewSearchManager()),
		Skills:       skills.NewManager(""),
		Plugins:      plugins.NewManager(tempDir, bus),
		MCP:          mcp.NewManager(tempDir),
		Bus:          bus,
		DataDir:      tempDir,
	})
	return mgr, tempDir
}

func brainTestSession(t *testing.T, mgr *Manager, ws string) *Session {
	t.Helper()
	sess, err := mgr.CreateSession("Brain Test", RoleCoding, ws)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// seedTurnMessages appends a finished-turn transcript to the session:
// user ask → assistant tool_use → tool failure → assistant correction →
// assistant decision prose → assistant summary.
func seedTurnMessages(sess *Session) {
	msg := func(role string, blocks []ContentBlock) AgentMessage {
		return AgentMessage{ID: role + "-" + time.Now().Format("150405.000000000"), Role: role, Content: blocks, Timestamp: time.Now()}
	}
	sess.Messages = append(sess.Messages,
		msg("user", []ContentBlock{{Type: "text", Text: "Add retry handling to the uploader"}}),
		msg("assistant", []ContentBlock{
			{Type: "tool_use", ToolCallID: "c1", Name: "edit", Arguments: map[string]any{"path": "/ws/uploader.go"}},
		}),
		msg("tool", []ContentBlock{
			{Type: "tool_result", ToolCallID: "c1", Name: "edit", Text: "exit 1: tests time out", IsError: true},
		}),
		msg("assistant", []ContentBlock{
			{Type: "text", Text: "The test failed because the fake clock never advanced, so the backoff timer waited forever."},
		}),
		msg("assistant", []ContentBlock{
			{Type: "text", Text: "We decided to use exponential backoff for the retries. Fixed the clock and all tests pass now."},
		}),
	)
	sess.Progress.CurrentGoal = "Make the uploader resilient to transient failures"
}

func TestBuildTurnRecord(t *testing.T) {
	sess := &Session{ID: "s1", Folder: "/ws"}
	seedTurnMessages(sess)
	rec := buildTurnRecord(sess)

	if rec.SessionID != "s1" {
		t.Fatalf("session id mismatch: %q", rec.SessionID)
	}
	if !strings.Contains(rec.UserText, "retry handling") {
		t.Fatalf("user text mismatch: %q", rec.UserText)
	}
	if !strings.HasPrefix(rec.TurnID, "s1:") {
		t.Fatalf("turn id should key on the boundary user message, got %q", rec.TurnID)
	}
	if !strings.Contains(rec.AssistantText, "exponential backoff") {
		t.Fatalf("assistant text missing: %q", rec.AssistantText)
	}
	if len(rec.Files) != 1 || rec.Files[0] != "/ws/uploader.go" {
		t.Fatalf("files mismatch: %v", rec.Files)
	}
	if len(rec.Corrections) == 0 || !strings.Contains(rec.Corrections[0], "because") {
		t.Fatalf("corrections missing: %v", rec.Corrections)
	}
	if len(rec.ToolTrace) != 1 || !rec.ToolTrace[0].IsError {
		t.Fatalf("tool trace mismatch: %+v", rec.ToolTrace)
	}
	if rec.Objective == "" {
		t.Fatal("objective should come from session progress")
	}
	if !strings.Contains(rec.FinalSummary, "tests pass") {
		t.Fatalf("final summary mismatch: %q", rec.FinalSummary)
	}
}

func TestBrainConsolidationWritesFactsAndDigest(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	ws := t.TempDir()
	sess := brainTestSession(t, mgr, ws)
	br, err := brain.Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mgr.SetBrain(br)

	seedTurnMessages(sess)
	mgr.mu.Lock()
	mgr.sessions[sess.ID] = sess
	mgr.mu.Unlock()

	// Synchronous consolidation (the engine path wraps this in a goroutine).
	mgr.consolidateBrain(sess.ID)

	facts := br.ValidFacts()
	if len(facts) == 0 {
		t.Fatal("expected facts after consolidation")
	}
	hasDecision := false
	for _, f := range facts {
		if f.Kind == brain.KindDecision && strings.Contains(f.Text, "exponential backoff") {
			hasDecision = true
		}
		if strings.Contains(f.Text, "```") {
			t.Fatalf("code leaked into facts: %q", f.Text)
		}
	}
	if !hasDecision {
		t.Fatalf("decision fact missing: %+v", facts)
	}
	digest, err := br.ReadDigest(sess.ID)
	if err != nil || !strings.Contains(digest, "transient failures") {
		t.Fatalf("digest missing/invalid: %v %q", err, digest)
	}
}

func TestConsolidationWithoutBrainIsNoop(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	ws := t.TempDir()
	sess := brainTestSession(t, mgr, ws)
	seedTurnMessages(sess)
	mgr.mu.Lock()
	mgr.sessions[sess.ID] = sess
	mgr.mu.Unlock()
	mgr.consolidateBrain(sess.ID) // must not panic with nil brain
}

func TestBrainInjectionCacheStability(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	ws := t.TempDir()
	sess := brainTestSession(t, mgr, ws)
	br, _ := brain.Open(ws)
	mgr.SetBrain(br)
	_, _ = br.AppendFact(brain.KindDecision, "Keep retry caps at five attempts", nil, "t:1")
	_, _ = br.AppendFact(brain.KindPitfall, "Reading the index while rebuilding it deadlocks", nil, "t:2")

	sess.Messages = append(sess.Messages,
		AgentMessage{ID: "u1", Role: "user", Content: []ContentBlock{{Type: "text", Text: "Fix the retry deadlock in uploader.go"}}, Timestamp: time.Now()},
	)

	// Two steps of the same turn must observe byte-identical brain context
	// (it is computed once per turn and reused across loop iterations).
	first := buildBrainContext(br, sess)
	time.Sleep(5 * time.Millisecond)
	second := buildBrainContext(br, sess)
	if first == "" {
		t.Fatal("expected non-empty brain context")
	}
	if first != second {
		t.Fatalf("brain context not byte-stable across steps:\n--\n%s\n--\n%s", first, second)
	}
	if !strings.Contains(first, "<project_brain>") || !strings.Contains(first, "budget:") {
		t.Fatalf("brain context missing block/header: %q", first)
	}
	// Applicability: retry/deadlock facts belong, unrelated ones rank below.
	if strings.Index(first, "deadlocks") > strings.Index(first, "retry caps") && strings.Contains(first, "retry caps") {
		t.Fatalf("applicability ordering wrong:\n%s", first)
	}

	// No user text / empty brain → empty context (no block injected).
	if got := buildBrainContext(nil, sess); got != "" {
		t.Fatalf("nil brain must yield empty context, got %q", got)
	}
	empty := &Session{ID: "e", Messages: nil}
	if got := buildBrainContext(br, empty); got != "" {
		t.Fatalf("expected empty brief with no facts, got %q", got)
	}
}

func TestBrainToolsRegisteredAndClassified(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	reg := mgr.toolReg
	for _, name := range []string{"write_note", "search_brain", "read_digest"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Fatalf("tool %s not registered", name)
		}
	}
	if md := tools.MetadataFor("search_brain"); !md.ReadOnly || md.NeedsApproval {
		t.Fatalf("search_brain should be read-only and silent, got %+v", md)
	}
	if md := tools.MetadataFor("write_note"); md.NeedsApproval || md.SideEffectScope != "session" {
		t.Fatalf("write_note should mirror learn_memory metadata, got %+v", md)
	}
	// Universal availability: brain tools bypass role tool whitelists.
	sess := brainTestSession(t, mgr, t.TempDir())
	sess.Tools = []string{"filesystem"}
	mgr.mu.Lock()
	mgr.sessions[sess.ID] = sess
	mgr.mu.Unlock()
	for _, name := range []string{"write_note", "search_brain", "read_digest"} {
		if !mgr.isToolAllowedForSession(sess, name) {
			t.Fatalf("%s should be universally allowed", name)
		}
	}
}

func TestBrainBridgeTools(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	ws := t.TempDir()
	sess := brainTestSession(t, mgr, ws)
	br, _ := brain.Open(ws)
	mgr.SetBrain(br)
	_ = br.WriteDigest("other-session", "# older digest")

	bridge := &sessionBridge{m: mgr, sessionID: sess.ID, ctx: context.Background()}

	note, err := bridge.WriteBrainNote("Runbook", "Restart the daemon with SIGUSR1.")
	if err != nil || note["path"] == "" {
		t.Fatalf("WriteBrainNote: %v %v", err, note)
	}
	facts, err := bridge.SearchBrain("SIGUSR1", 8)
	if err != nil || len(facts) != 1 {
		t.Fatalf("SearchBrain: %v %+v", err, facts)
	}
	digest, err := bridge.ReadBrainDigest("other-session")
	if err != nil || !strings.Contains(digest["content"].(string), "older digest") {
		t.Fatalf("ReadBrainDigest(id): %v %v", err, digest)
	}
	latest, err := bridge.ReadBrainDigest("")
	if err != nil {
		t.Fatalf("ReadBrainDigest(latest): %v", err)
	}
	if latest["session_id"] != "other-session" {
		t.Fatalf("latest digest should be other-session, got %v", latest["session_id"])
	}

	// Tools executed through the registry get the same behavior.
	ctx := tools.WithSessionBridge(context.Background(), bridge)
	spec, _ := mgr.toolReg.Lookup("search_brain")
	res, err := spec.Handler(ctx, map[string]any{"query": "SIGUSR1"})
	if err != nil {
		t.Fatalf("search_brain handler: %v", err)
	}
	_ = res
	spec, _ = mgr.toolReg.Lookup("write_note")
	if _, err := spec.Handler(ctx, map[string]any{"title": "T", "body": "B"}); err != nil {
		t.Fatalf("write_note handler: %v", err)
	}
}

func TestBrainBridgeWithoutBrainErrors(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	ws := t.TempDir()
	sess := brainTestSession(t, mgr, ws)
	bridge := &sessionBridge{m: mgr, sessionID: sess.ID, ctx: context.Background()}
	if _, err := bridge.WriteBrainNote("t", "b"); err == nil {
		t.Fatal("expected error without brain attached")
	}
	if _, err := bridge.SearchBrain("x", 5); err == nil {
		t.Fatal("expected error without brain attached")
	}
	if _, err := bridge.ReadBrainDigest(""); err == nil {
		t.Fatal("expected error without brain attached")
	}
}
