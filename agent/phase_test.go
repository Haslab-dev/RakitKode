package agent

import (
	"strings"
	"testing"
	"time"
)

func phaseSession(msgs []AgentMessage, tasks []TaskItem) *Session {
	return &Session{ID: "p", Messages: msgs, Tasks: tasks, Progress: SessionProgress{CurrentGoal: "ship it"}}
}

func TestInferSessionPhase(t *testing.T) {
	now := time.Now()
	u := func(text string) AgentMessage {
		return AgentMessage{ID: text, Role: "user", Content: []ContentBlock{{Type: "text", Text: text}}, Timestamp: now}
	}
	a := func(tool string) AgentMessage {
		return AgentMessage{ID: tool + now.String(), Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolCallID: "c", Name: tool, Arguments: map[string]any{}}}, Timestamp: now}
	}

	t.Run("wrapup when all tasks completed", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("wrap it up"), a("read")}, []TaskItem{
			{ID: "1", Status: TaskCompleted},
			{ID: "2", Status: TaskCompleted},
		})
		if got := inferSessionPhase(sess); got != PhaseWrapup {
			t.Fatalf("expected wrapup, got %s", got)
		}
	})

	t.Run("debugging on failure keywords", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("the build is broken, fix the bug"), a("read")}, nil)
		if got := inferSessionPhase(sess); got != PhaseDebugging {
			t.Fatalf("expected debugging, got %s", got)
		}
	})

	t.Run("verification when tests run after edits", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("add the feature"), a("edit"), a("bash")}, nil)
		if got := inferSessionPhase(sess); got != PhaseVerification {
			t.Fatalf("expected verification, got %s", got)
		}
	})

	t.Run("implementation on mutating tools", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("add the feature"), a("edit"), a("write")}, nil)
		if got := inferSessionPhase(sess); got != PhaseImplementation {
			t.Fatalf("expected implementation, got %s", got)
		}
	})

	t.Run("verification on bare shell run", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("add the feature"), a("bash")}, nil)
		if got := inferSessionPhase(sess); got != PhaseVerification {
			t.Fatalf("expected verification, got %s", got)
		}
	})

	t.Run("planning on task tools", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("add the feature"), a("update_tasks")}, nil)
		if got := inferSessionPhase(sess); got != PhasePlanning {
			t.Fatalf("expected planning, got %s", got)
		}
	})

	t.Run("discovery default", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("add the feature"), a("read"), a("search")}, nil)
		if got := inferSessionPhase(sess); got != PhaseDiscovery {
			t.Fatalf("expected discovery, got %s", got)
		}
	})

	t.Run("in-progress tasks block wrapup", func(t *testing.T) {
		sess := phaseSession([]AgentMessage{u("continue"), a("read")}, []TaskItem{
			{ID: "1", Status: TaskCompleted},
			{ID: "2", Status: TaskInProgress, ActiveForm: "editing"},
		})
		if got := inferSessionPhase(sess); got == PhaseWrapup {
			t.Fatal("in-progress tasks must block wrapup")
		}
	})
}

func TestBuildSessionPhaseContext(t *testing.T) {
	now := time.Now()
	sess := &Session{ID: "p", Messages: []AgentMessage{
		{ID: "u", Role: "user", Content: []ContentBlock{{Type: "text", Text: "the tests are broken"}}, Timestamp: now},
		{ID: "a", Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolCallID: "c", Name: "read", Arguments: map[string]any{}}}, Timestamp: now},
	}}
	ctx := buildSessionPhaseContext(sess)
	if !strings.Contains(ctx, "phase: debugging") {
		t.Fatalf("expected debugging phase line, got %q", ctx)
	}
	if !strings.Contains(ctx, "<session_phase>") {
		t.Fatalf("missing block tags: %q", ctx)
	}
	if got := buildSessionPhaseContext(nil); got != "" {
		t.Fatalf("nil session must yield empty context, got %q", got)
	}
	// Byte-stable across steps within a turn.
	if ctx != buildSessionPhaseContext(sess) {
		t.Fatal("phase context must be byte-stable")
	}
}

func TestPhaseLineInOrchestrationContext(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	now := time.Now()
	sess := &Session{ID: "p", RoleFilter: RoleOrchestrator, Messages: []AgentMessage{
		{ID: "u", Role: "user", Content: []ContentBlock{{Type: "text", Text: "fix the failing test"}}, Timestamp: now},
		{ID: "a", Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolCallID: "c", Name: "edit", Arguments: map[string]any{}}}, Timestamp: now},
		{ID: "a2", Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolCallID: "c2", Name: "bash", Arguments: map[string]any{}}}, Timestamp: now},
	}}
	// Enable orchestration (defaults on for orchestrator sessions in tests? Force it.)
	cfg := mgr.GetOrchestrationConfig()
	cfg.Enabled = true
	if err := mgr.SaveOrchestrationConfig(cfg); err != nil {
		t.Fatalf("SaveOrchestrationConfig: %v", err)
	}
	ctx := mgr.buildOrchestrationContext(sess)
	if !strings.Contains(ctx, "phase:") {
		t.Fatalf("orchestration context should carry a phase line, got:\n%s", ctx[:200])
	}
}
