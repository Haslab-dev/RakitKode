package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Haslab-dev/RakitKode/events"
	"github.com/Haslab-dev/RakitKode/llm"
	"github.com/Haslab-dev/RakitKode/search"
	"github.com/Haslab-dev/RakitKode/tools"
)

func TestParseGoalCommand(t *testing.T) {
	tests := []struct {
		input     string
		wantKind  GoalCommandKind
		wantObj   string
		wantSteps int
	}{
		{"", GoalCmdStatus, "", DefaultMaxGoalContinuations},
		{"   ", GoalCmdStatus, "", DefaultMaxGoalContinuations},
		{"clear", GoalCmdClear, "", DefaultMaxGoalContinuations},
		{"RESET", GoalCmdClear, "", DefaultMaxGoalContinuations},
		{"off", GoalCmdClear, "", DefaultMaxGoalContinuations},
		{"from-chat", GoalCmdFromChat, "", DefaultMaxGoalContinuations},
		{"from-chat include auth tests", GoalCmdFromChat, "include auth tests", DefaultMaxGoalContinuations},
		{"implement oauth2 flow", GoalCmdSet, "implement oauth2 flow", DefaultMaxGoalContinuations},
		{"  fix memory leak and pass benchmarks  ", GoalCmdSet, "fix memory leak and pass benchmarks", DefaultMaxGoalContinuations},
		{"100 fix bug in auth", GoalCmdSet, "fix bug in auth", 100},
		{"max=50 refactor engine", GoalCmdSet, "refactor engine", 50},
		{"fix tests sampai selesai", GoalCmdSet, "fix tests sampai selesai", 100},
	}

	for _, tt := range tests {
		cmd := ParseGoalCommand(tt.input)
		if cmd.Kind != tt.wantKind {
			t.Errorf("ParseGoalCommand(%q).Kind = %v, want %v", tt.input, cmd.Kind, tt.wantKind)
		}
		if cmd.Objective != tt.wantObj {
			t.Errorf("ParseGoalCommand(%q).Objective = %q, want %q", tt.input, cmd.Objective, tt.wantObj)
		}
		if cmd.MaxContinuations != tt.wantSteps {
			t.Errorf("ParseGoalCommand(%q).MaxContinuations = %d, want %d", tt.input, cmd.MaxContinuations, tt.wantSteps)
		}
	}
}

func TestBuildGoalState(t *testing.T) {
	goal := BuildGoalState("deliver feature X")
	if goal.Objective != "deliver feature X" {
		t.Fatalf("unexpected objective: %s", goal.Objective)
	}
	if goal.Status != "active" {
		t.Fatalf("unexpected status: %s", goal.Status)
	}
	if goal.ContinuationCount != 0 {
		t.Fatalf("continuation count should be 0")
	}
	if goal.MaxContinuations != DefaultMaxGoalContinuations {
		t.Fatalf("expected max continuations %d, got %d", DefaultMaxGoalContinuations, goal.MaxContinuations)
	}
	if goal.MaxNoProgressContinuations != DefaultMaxNoProgressContinuations {
		t.Fatalf("expected max no-progress %d, got %d", DefaultMaxNoProgressContinuations, goal.MaxNoProgressContinuations)
	}

	custom := BuildGoalState("test custom", 15)
	if custom.MaxContinuations != 15 {
		t.Fatalf("expected custom max continuations 15, got %d", custom.MaxContinuations)
	}
}

func TestParseGoalEvaluationResponse(t *testing.T) {
	// 1. Clean JSON with satisfied true
	cleanJSON := `{"satisfied": true, "blocker": "none", "reason": "All tests pass", "evidence_summary": "bash exit code 0", "relied_on_assumption": false}`
	eval, err := ParseGoalEvaluationResponse(cleanJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !eval.Satisfied {
		t.Errorf("expected satisfied = true")
	}
	if eval.Blocker != GoalBlockerNone {
		t.Errorf("expected blocker = none, got %s", eval.Blocker)
	}
	if eval.Reason != "All tests pass" {
		t.Errorf("unexpected reason: %s", eval.Reason)
	}

	// 2. Markdown fenced with think tags
	fenced := "<think>Analyzing results...</think>\n```json\n{\n  \"satisfied\": false,\n  \"blocker\": \"goal_not_met_yet\",\n  \"reason\": \"Tests not run yet\",\n  \"evidence_summary\": \"Files edited but tests pending\"\n}\n```"
	eval2, err := ParseGoalEvaluationResponse(fenced)
	if err != nil {
		t.Fatalf("unexpected error parsing fenced: %v", err)
	}
	if eval2.Satisfied {
		t.Errorf("expected satisfied = false")
	}
	if eval2.Blocker != GoalBlockerNotMetYet {
		t.Errorf("expected blocker = goal_not_met_yet, got %s", eval2.Blocker)
	}

	// 3. Needs user input blocker
	needsInput := `{"satisfied": false, "blocker": "needs_user_input", "reason": "Missing API key"}`
	eval3, err := ParseGoalEvaluationResponse(needsInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eval3.Blocker != GoalBlockerNeedsUserInput {
		t.Errorf("expected blocker = needs_user_input, got %s", eval3.Blocker)
	}

	// 4. Invalid JSON
	_, err = ParseGoalEvaluationResponse("No JSON here!")
	if err == nil {
		t.Errorf("expected error for non-JSON response")
	}
}

func TestSuccessfulOperationKey(t *testing.T) {
	args1 := map[string]any{"command": "go test ./...", "description": "Run unit tests"}
	args2 := map[string]any{"command": "go test ./..."}
	out := "PASS\nok  test 0.1s"

	// Description should be ignored for bash tools
	key1 := SuccessfulOperationKey("bash", args1, out)
	key2 := SuccessfulOperationKey("bash", args2, out)
	if key1 != key2 {
		t.Errorf("expected key1 == key2 when description is ignored, got %q vs %q", key1, key2)
	}

	// Changing output must change key
	key3 := SuccessfulOperationKey("bash", args2, "FAIL")
	if key1 == key3 {
		t.Errorf("expected different key for different output")
	}
}

func TestObserveGoalOperations(t *testing.T) {
	goal := BuildGoalState("test progress")

	calls := []ContentBlock{
		{ToolCallID: "call-1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}},
		{ToolCallID: "call-2", Name: "bash", Arguments: map[string]any{"command": "go test"}},
	}
	results := map[string]ContentBlock{
		"call-1": {ToolCallID: "call-1", Name: "read_file", Text: "package main"},
		"call-2": {ToolCallID: "call-2", Name: "bash", Text: "ok", IsError: false},
	}

	hasActivity, newKeys := ObserveGoalOperations(goal, calls, results)
	if !hasActivity {
		t.Fatalf("expected tool activity")
	}
	if newKeys != 2 {
		t.Fatalf("expected 2 new keys, got %d", newKeys)
	}

	// Repeating identical operations should produce 0 new keys
	hasActivity2, newKeys2 := ObserveGoalOperations(goal, calls, results)
	if !hasActivity2 {
		t.Fatalf("expected tool activity")
	}
	if newKeys2 != 0 {
		t.Fatalf("expected 0 new keys for repeated operations, got %d", newKeys2)
	}

	// Error tool results should not produce new success keys
	errCalls := []ContentBlock{
		{ToolCallID: "call-3", Name: "bash", Arguments: map[string]any{"command": "bad cmd"}},
	}
	errResults := map[string]ContentBlock{
		"call-3": {ToolCallID: "call-3", Name: "bash", Text: "command not found", IsError: true},
	}
	_, errKeys := ObserveGoalOperations(goal, errCalls, errResults)
	if errKeys != 0 {
		t.Fatalf("expected 0 new keys for error tool result, got %d", errKeys)
	}
}

func TestShouldContinueGoal(t *testing.T) {
	goal := BuildGoalState("test continuation")

	// 1. Goal satisfied -> false
	if ShouldContinueGoal(goal, &GoalEvaluation{Satisfied: true, Blocker: GoalBlockerNone}) {
		t.Errorf("satisfied goal should not continue")
	}

	// 2. Goal blocker needs_user_input -> false
	if ShouldContinueGoal(goal, &GoalEvaluation{Satisfied: false, Blocker: GoalBlockerNeedsUserInput}) {
		t.Errorf("needs_user_input blocker should not continue automatically")
	}

	// 3. Goal blocker goal_not_met_yet -> true
	if !ShouldContinueGoal(goal, &GoalEvaluation{Satisfied: false, Blocker: GoalBlockerNotMetYet}) {
		t.Errorf("goal_not_met_yet with remaining budget should continue")
	}

	// 4. Continuation budget exhausted -> false
	goal.ContinuationCount = goal.MaxContinuations
	if ShouldContinueGoal(goal, &GoalEvaluation{Satisfied: false, Blocker: GoalBlockerNotMetYet}) {
		t.Errorf("exhausted continuation budget should not continue")
	}

	// 5. No progress count exhausted -> false
	goal.ContinuationCount = 2
	goal.NoProgressCount = 2
	if ShouldContinueGoal(goal, &GoalEvaluation{Satisfied: false, Blocker: GoalBlockerNotMetYet}) {
		t.Errorf("exhausted no-progress count should not continue")
	}
}

func TestFormatGoalEvidence(t *testing.T) {
	msgs := []AgentMessage{
		{
			Role: "user",
			Content: []ContentBlock{
				{Type: "text", Text: "Please fix bug in login handler"},
			},
			Timestamp: time.Now(),
		},
		{
			Role: "assistant",
			Content: []ContentBlock{
				{Type: "text", Text: "I will check login.go"},
				{Type: "tool_call", ToolCallID: "c1", Name: "read_file", Arguments: map[string]any{"path": "login.go"}},
			},
			Timestamp: time.Now(),
		},
		{
			Role: "tool",
			Content: []ContentBlock{
				{Type: "tool_result", ToolCallID: "c1", Name: "read_file", Text: "func Login() { ... }"},
			},
			Timestamp: time.Now(),
		},
	}

	evidence := FormatGoalEvidence(msgs, 5000)
	if !strings.Contains(evidence, "User: Please fix bug in login handler") {
		t.Errorf("evidence missing user request")
	}
	if !strings.Contains(evidence, "Assistant: I will check login.go") {
		t.Errorf("evidence missing assistant reply")
	}
	if !strings.Contains(evidence, "Assistant tool call: read_file") {
		t.Errorf("evidence missing tool call")
	}
	if !strings.Contains(evidence, "Tool result (read_file):") {
		t.Errorf("evidence missing tool result")
	}
}

func TestMakeGoalContinuationMessage(t *testing.T) {
	goal := BuildGoalState("complete refactor")
	eval := &GoalEvaluation{
		Satisfied:       false,
		Blocker:         GoalBlockerNotMetYet,
		Reason:          "unit tests need to be run",
		EvidenceSummary: "code updated",
	}

	msg := MakeGoalContinuationMessage(goal, eval)
	if msg.Role != "user" {
		t.Errorf("continuation message role should be user, got %s", msg.Role)
	}
	text := msg.Text()
	if !strings.Contains(text, "<goal_continuation>") {
		t.Errorf("missing goal_continuation tag")
	}
	if !strings.Contains(text, "step 1 of ") {
		t.Errorf("missing step count")
	}
	if !strings.Contains(text, "unit tests need to be run") {
		t.Errorf("missing reason in continuation prompt")
	}
}

func TestPrematureExitTaskGuard(t *testing.T) {
	sess := &Session{
		ID: "sess-test-guard",
		Tasks: []TaskItem{
			{ID: "1", Title: "Read source", Status: TaskCompleted, Completed: true},
			{ID: "2", Title: "Implement fix", Status: TaskPending},
		},
		Messages: []AgentMessage{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "fix the bug"}}},
		},
	}

	var incompleteList []string
	for _, task := range sess.Tasks {
		if task.Status == TaskPending || task.Status == TaskInProgress || (!task.Completed && task.Status != TaskCompleted) {
			incompleteList = append(incompleteList, "- ["+task.ID+"] "+task.Title)
		}
	}

	if len(incompleteList) != 1 {
		t.Fatalf("expected 1 incomplete task, got %d", len(incompleteList))
	}
	if !strings.Contains(incompleteList[0], "Implement fix") {
		t.Errorf("incomplete task should be 'Implement fix', got %s", incompleteList[0])
	}
}

func TestGoalLifecycleManagement(t *testing.T) {
	tempDir := t.TempDir()
	bus := events.NewBus()
	llmClient := llm.NewLLMClient(tempDir)
	toolReg := tools.NewRegistry(search.NewSearchManager())
	m := NewManager(llmClient, toolReg, nil, nil, nil, nil, bus, tempDir)
	sess, err := m.CreateSession("test-workspace", RoleCoding, tempDir)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	// 1. Set goal
	goal, err := m.SetGoal(sess.ID, "build and test microservice")
	if err != nil {
		t.Fatalf("SetGoal failed: %v", err)
	}
	if goal.Objective != "build and test microservice" {
		t.Errorf("unexpected objective: %s", goal.Objective)
	}

	// 2. Get goal
	retrieved, ok := m.GetGoal(sess.ID)
	if !ok || retrieved == nil {
		t.Fatalf("GetGoal should return active goal")
	}
	if retrieved.Status != "active" {
		t.Errorf("expected status active, got %s", retrieved.Status)
	}

	// 3. Clear goal
	if !m.ClearGoal(sess.ID) {
		t.Fatalf("ClearGoal should succeed")
	}
	_, okAfterClear := m.GetGoal(sess.ID)
	if okAfterClear {
		t.Errorf("goal should be cleared")
	}
}

func TestDetectGoalIntent(t *testing.T) {
	tests := []struct {
		input     string
		wantGoal  bool
		wantSteps int
	}{
		{"halo apa kabar", false, 0},
		{"jelaskan arsitektur codebase ini", false, 0},
		{"tolong jelaskan bagaimana cara deploy", false, 0},
		{"siapa nama kamu?", false, 0},
		{"terima kasih banyak", false, 0},
		{"tolong fix bug login", true, DefaultMaxGoalContinuations},
		{"perbaiki test yang fail", true, DefaultMaxGoalContinuations},
		{"tolong buatkan endpoint auth baru", true, DefaultMaxGoalContinuations},
		{"implement oauth2 handler", true, DefaultMaxGoalContinuations},
		{"tolong selesaikan masalah ini sampai selesai", true, 100},
		{"fix all failing tests until finished", true, 100},
		{"tolong refactor kode ini meskipun ratusan step", true, 100},
	}

	for _, tt := range tests {
		isGoal, _, steps := DetectGoalIntent(tt.input)
		if isGoal != tt.wantGoal {
			t.Errorf("DetectGoalIntent(%q).isGoal = %v, want %v", tt.input, isGoal, tt.wantGoal)
		}
		if tt.wantGoal && steps != tt.wantSteps {
			t.Errorf("DetectGoalIntent(%q).steps = %d, want %d", tt.input, steps, tt.wantSteps)
		}
	}
}

func TestAutoGoalInSendMessage(t *testing.T) {
	tempDir := t.TempDir()
	bus := events.NewBus()
	llmClient := llm.NewLLMClient(tempDir)
	toolReg := tools.NewRegistry(search.NewSearchManager())
	m := NewManager(llmClient, toolReg, nil, nil, nil, nil, bus, tempDir)
	sess, err := m.CreateSession("test-autogoal", RoleCoding, tempDir)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer m.StopTurn(sess.ID)

	// 1. Send normal conversation - should not activate goal
	_ = m.SendMessage(context.Background(), sess.ID, "halo, siapa kamu?", nil)
	if goal, ok := m.GetGoal(sess.ID); ok && goal != nil && goal.Status == "active" {
		t.Errorf("casual greeting should not activate goal")
	}

	// 2. Send actionable task with "sampai selesai" - should auto-activate goal with 100 steps!
	_ = m.SendMessage(context.Background(), sess.ID, "tolong fix bug koneksi database sampai selesai", nil)
	goal, ok := m.GetGoal(sess.ID)
	if !ok || goal == nil {
		t.Fatalf("expected goal to be auto-activated")
	}
	if goal.Status != "active" {
		t.Errorf("expected goal status active, got %s", goal.Status)
	}
	if goal.MaxContinuations != 100 {
		t.Errorf("expected 100 max continuations for 'sampai selesai', got %d", goal.MaxContinuations)
	}
	if !strings.Contains(goal.Objective, "tolong fix bug koneksi database") {
		t.Errorf("unexpected objective: %s", goal.Objective)
	}
}

func TestSetMaxSteps(t *testing.T) {
	tempDir := t.TempDir()
	bus := events.NewBus()
	llmClient := llm.NewLLMClient(tempDir)
	toolReg := tools.NewRegistry(search.NewSearchManager())
	m := NewManager(llmClient, toolReg, nil, nil, nil, nil, bus, tempDir)
	sess, err := m.CreateSession("test-maxsteps", RoleCoding, tempDir)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Default should be DefaultMaxReasoningSteps
	if got := m.MaxStepsFor(sess.ID); got != DefaultMaxReasoningSteps {
		t.Errorf("expected default %d, got %d", DefaultMaxReasoningSteps, got)
	}

	// Set to -1 (unlimited)
	if err := m.SetMaxSteps(sess.ID, -1); err != nil {
		t.Fatalf("SetMaxSteps failed: %v", err)
	}
	if got := m.MaxStepsFor(sess.ID); got != -1 {
		t.Errorf("expected -1, got %d", got)
	}

	// Set to 500
	if err := m.SetMaxSteps(sess.ID, 500); err != nil {
		t.Fatalf("SetMaxSteps failed: %v", err)
	}
	if got := m.MaxStepsFor(sess.ID); got != 500 {
		t.Errorf("expected 500, got %d", got)
	}
}
