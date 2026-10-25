package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackgroundStoreRestoresLostTasks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "background_tasks.json")

	r := NewRegistry(nil)
	r.SetBackgroundStore(path)
	r.RestoreTasks([]*BackgroundTask{{
		ID:         "abc123",
		SessionID:  "s1",
		Command:    "sleep 100",
		Status:     TaskRunning,
		StartedAt:  time.Now().Add(-time.Minute),
		OutputFile: filepath.Join(dir, "task.log"),
		OutputTail: "partial",
	}})

	task, ok := r.GetTask("abc123")
	if !ok {
		t.Fatal("task not restored")
	}
	if task.Status != TaskLost {
		t.Fatalf("status = %s, want lost", task.Status)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("store not written: %v", err)
	}

	// A second registry loading the same file must not duplicate or resurrect.
	r2 := NewRegistry(nil)
	r2.SetBackgroundStore(path)
	task2, ok := r2.GetTask("abc123")
	if !ok {
		t.Fatal("task not reloaded")
	}
	if task2.Status != TaskLost {
		t.Fatalf("reloaded status = %s", task2.Status)
	}
}

func TestBackgroundStoreTerminalTaskStays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "background_tasks.json")
	now := time.Now()
	r := NewRegistry(nil)
	r.SetBackgroundStore(path)
	r.RestoreTasks([]*BackgroundTask{{
		ID:        "done1",
		SessionID: "s1",
		Command:   "echo hi",
		Status:    TaskCompleted,
		ExitCode:  0,
		StartedAt: now.Add(-time.Minute),
		Finished:  &now,
	}})
	task, ok := r.GetTask("done1")
	if !ok || task.Status != TaskCompleted {
		t.Fatalf("terminal task not preserved: %+v", task)
	}
}
